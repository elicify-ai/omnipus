# Spec #1931: A session's kind comes from `type`; `channel` only says where it lives; replies follow the turn's origin

- **Status:** Draft for test authoring, revised after grill round 1 (BLOCK; `/workspace/logs/1931/spec/grill-round1.md`; changes in `/workspace/logs/1931/spec/fix-round1-changelog.md`) and grill round 2 (BLOCK; `/workspace/logs/1931/spec/grill-round2.md`; changes in `/workspace/logs/1931/spec/fix-round2-changelog.md`). All founder decisions applied (2026-11-28 06:03Z); no conditional requirements remain.
- **Date:** 2026-11-28
- **Issue:** #1931 (P0, open), "Mid-turn redirect is refused in every Main chat (channel main, no peer id)"
- **Grounding ADR:** `/workspace/logs/1931/adr-session-kind-vs-channel.md` (see its 2026-11-28 amendment). D1-D4, D6, D7 as written. D5 replaced by the founder's Q1 answer (one-time script, no read-time clean-up). D8 kept, reworded for the script. D3's web-view corner fixed to today's behaviour (Q2); D3 extended for a Main redirect typed in a connector (FR-028). Architect review: `/workspace/logs/1931/architect-review.md`.
- **Code read:** `origin/release/v0.1.1` at `ef6845a5d` (re-read 2026-11-28 after grill rounds 1 and 2). ⚠️ Correction (round 2, R2-N4): round 1 said only three cited files changed since `d9cfddc19`; that was wrong. `git diff --stat d9cfddc19 ef6845a5d -- <cited files>` lists eight: `.github/workflows/cross-platform.yml`, `.github/workflows/pr.yml`, `pkg/agent/loop.go`, `pkg/agent/turn.go`, `pkg/api/generated/openapi_types.gen.go`, `pkg/gateway/rest_sessions.go`, `src/lib/api/generated/openapi-types.ts`, `src/lib/api/sessions.ts` (plus `pkg/channels/discord/*` inside the cited `pkg/channels/` folder). None of these changes alters a fact this spec relies on; the moved locations are corrected below. Symbols (`file::symbol`) are the citation; line numbers are snapshot locators for `ef6845a5d` only. Read only; nothing built or run.

## Summary (plain English)

1. Today `/stop-redirect` in a Main chat is refused ("Not stopped. Press Stop again.") because Main stores the word "main" where the chat's place should be, and the server reads that as "an outside chat with no address".
2. After the fix, one rule in one place answers "is this a real connector chat (Telegram etc.) with a known chat id?". Only then do replies go outside; every other chat answers in its own web chat.
3. A redirect typed in Telegram answers in Telegram; one sent from the web answers in the web. A Telegram conversation redirected from its web view keeps going to Telegram, exactly as today. In a Main reached through Telegram, the redirect becomes a normal Telegram request of the Main, answered the way every Main answers Telegram today (by answering that request); a plain answer is still never sent to Telegram.
4. New Main, scheduled, verifier and task chats store an empty place. Old stored labels are cleared once on the live instance by a one-time script (dry run first, backup first, safe to run twice). The product gets no upgrade logic. A backup is restored only before the instance runs again; after that, rolling back means installing the previous build only.
5. Nothing else changes: plain mid-turn messages still queue, no new field, no frontend logic change. The user guide's stop/redirect section gains a few sentences on where the answer appears.

## Founder decisions applied (2026-11-28)

| Question | Answer | Effect in this spec |
|---|---|---|
| Q1 (old labels, D5/D8) | One-time script for the live instance only; no read-time clean-up or rewrite-on-save in the product; PW-D1 amended, the script is the one sanctioned rewrite | FR-011 (product reads stored values unchanged), FR-019..FR-027 (script and rollout), FR-018 (PW-D1) |
| Q2 (web view of a connector chat, D3) | Keep today's behaviour exactly; pin it with a regression scenario | FR-007, Scenario 6, T-18 |
| General | "Let's not completely reinvent the wheel": minimal change, reuse existing paths | Earlier draft items removed: read-time tolerance, quiet rewrite, rollback-predicate unit test, frame-size rows, extra wake and queue tests already covered elsewhere |

**Correction to the premise in the founder note.** The note says an old session with channel `"main"` "would not be recognised by the new shared rule" until the script runs. Under ADR D2 (final) the rule keys on `type`, not on the stored label: a Main, scheduled, verifier or task session never has an outside address, whatever its stored `channel` says. So **routing is correct even before the script runs** (🔶 follows from FR-001, not run; pinned by T-02's `legacy_main_label` subtest, T-20's legacy Main and DS-1 rows 2, 3, 5, 6, 8, 11). What is wrong until the script runs is the **stored and API value**: `GET /api/v1/sessions` keeps showing `channel: "main"` for old sessions, which contradicts the new contract text (FR-015). The rollout order (FR-026) is still a hard requirement, for that reason and because a running build would overwrite the script's change on its next save of that session.

## Glossary

- **Kind**: the session's `type` (`main`, `chat`, `task`, `channel`, `scheduled`, `heartbeat`, `verifier`, `delegate`; `pkg/session/unified.go:20-70`).
- **Place**: the session's stored `channel`.
- **Peer id**: the outside chat id stored on the session (`PeerID`); only `NewChannelSession` sets it.
- **Outside reply address**: (place, peer id, instance id) that replies can be sent to outside the web app.
- **Stated origin**: the channel and chat id the redirect trigger carries. The web redirect frame states `"web"` and no chat id (`pkg/gateway/websocket_redirect.go:46`), which counts as "not stated". A connector command states its channel and chat id (`pkg/agent/command_channel_controls.go:70`).
- **Legacy labels**: exactly `main`, `scheduled`, `verifier`, `system`, `heartbeat`.
- **Home**: the instance's Omnipus home folder; the shared session store is `<home>/sessions/<session id>/meta.json` (`pkg/agent/shared_session_store.go:65`).

## Existing Codebase Context

### Symbols involved

| Symbol | Role | Context |
|---|---|---|
| `ordinaryRedirectRoute` (`pkg/agent/stop_redirect_root.go:110`) | modifies | Web-frame route; refuses a non-`webchat` place without peer id (the bug) |
| `wakeStreamAddress` (`pkg/agent/wake_address.go:8`) | modifies | Where a woken turn streams; callers `steer_reconstruct.go:99`, `wake_session.go:169` |
| `reportingTargetFor` (`pkg/agent/steer_launcher.go:657`) | modifies | Where a delegated child reports back; caller `steer_launcher.go:527` |
| `rebuildChannelSessionIndex` (`pkg/agent/loop_session.go:46`) | modifies | Boot-time index of connector chats |
| `createMainSessionLocked`, `NewScheduledSession`, `GetOrCreateScheduledSession`, `NewVerifierSession` (`pkg/session/unified.go:812,738,886,849`) | modifies | Write kind labels today |
| `createTaskSessionSyncAs` (`pkg/agent/task_executor.go:845`), plan engine session mint (`pkg/agent/plan_engine.go:2285`) | modifies | Write `"system"` today |
| `UnifiedMeta` (`pkg/session`) | extends | Gains `OutsideReplyAddress()` (FR-001) |
| `readUnifiedMeta`, `u5WriteIdentityLocked` | unchanged | Reader and writer stay as they are (FR-011); identity file is `json.MarshalIndent(..., "", "  ")`, mode `0o600`, atomic write (`unified_meta_files.go:354-363`) |
| `RedirectSessionTurn`, `RequestRedirectByChannelChat` | calls | Entry points: web frame and connector command (`command_channel_controls.go:60-73`), unchanged |
| `continueOrdinaryAfterStop` (`pkg/agent/stop_redirect_root.go:141`) | modifies | Gains one sibling of its webchat transcript branch (lines 157-173): a Main continuation on a connector is admitted as a captured request, withdrawn again if its turn cannot start (FR-028) |
| `admitBoundConnectorInputCtx` (`pkg/agent/connector_intake.go:97`) | modifies (minimal) | Existing bound-connector admission: capture, transcript entry, model envelope. Its body moves unchanged into a sibling `admitBoundConnectorInputForMain(ctx, msg, wantMainID)` that adds one check on the same config snapshot before any write; `admitBoundConnectorInputCtx` calls it with `""` (today's behaviour) (FR-028 (a), SC-008) |
| `AgentLoop.settleRequest` (`request_reminder.go`), `addressing.Ledger.Discard` (`pkg/addressing/ledger.go`) | calls | Existing withdrawal of a request, as admission already does after a failed append; used when a captured redirect cannot start (FR-028 (a3)) |
| `publishResponseIfNeeded` / `mainConnectorTurn` / `refuseMainConnectorDefaultSend` (`loop_publish.go:51-55`, `connector_egress.go:50-109`) | unchanged | Main default-send refusal stays exactly as it is |
| `AddressRouter.Reply` → `replyViaConnector` (`pkg/agent/address_router.go`) | unchanged | The existing answer path that reaches a connector from a Main |
| Session list mapping (`pkg/gateway/rest_sessions.go:257`) | calls | Passes `channel` through, unchanged |
| `contracts/components/schemas/Session.yaml`, `pkg/gateway/inboundschemas/Session.yaml` | modifies | Description text only |

### Impact assessment

| Symbol modified | Risk | Direct dependents | Indirect |
|---|---|---|---|
| `ordinaryRedirectRoute` | MEDIUM | `redirectOrdinarySession` | web redirect frame, connector `/stop-redirect`, existing G1/R1/R2 tests |
| `wakeStreamAddress` | MEDIUM | `steer_reconstruct.go`, `wake_session.go` | parent-wake (PW-D1) streaming |
| `reportingTargetFor` | MEDIUM | `SteerLauncher.Launch` | delegation completion wakes |
| `rebuildChannelSessionIndex` | LOW | boot | connector conversation reuse (winner ordering kept, FR-005) |
| `continueOrdinaryAfterStop` (FR-028 branch) | MEDIUM (security boundary) | Main redirect typed in a bound connector | connector request ledger, answer path; default-send refusal untouched |
| `admitBoundConnectorInputCtx` → `admitBoundConnectorInputForMain` | MEDIUM (security boundary) | `loop.go::admitAndDispatch` (passes `""`, unchanged behaviour), the FR-028 branch | every bound connector message; pinned by the unchanged `connector_intake_test.go` |
| Session creators | MEDIUM | every Main, scheduled, verifier, task creation | REST list output, Test Lab checks reading `channel` |
| One-time script | MEDIUM (live data) | live Mac instance, Fly test gateway | every stored session identity file it rewrites |

### Relevant execution flows

| Flow | Relevance |
|---|---|
| Web redirect: composer → WS `redirect` frame → `RedirectSessionTurn(..., "web")` → `redirectOrdinarySession` → `ordinaryRedirectRoute` | The bug path (Scenario 1) |
| Connector redirect: channel adapter (after its sender allow check, `pkg/channels/controlparse.go:68-79`) → `RequestRedirectByChannelChat(instance, chatID, senderID, ...)` → stated route | Route selection unchanged (FR-006) |
| Main connector redirect: … → stated route on a Main → after the stop, admission (pinned to that Main, one config snapshot) captures the instruction → one turn → the agent answers that request → `replyViaConnector` → that chat | FR-028 (today the continuation is a plain message with no request; its plain answer is refused and nothing is captured for it) |
| Parent wake → `wakeStreamAddress` | PW-D1 must keep streaming in the web chat |
| Delegation launch → `reportingTargetFor` | Report-back address |
| Boot → `rebuildChannelSessionIndex` | Connector conversation reuse after restart |

## User Stories

### US-1 (P0) Redirect works in Main and other kind-only chats
A person in a Main (or scheduled, task, verifier) chat in the web app sends `/stop-redirect <text>` and gets the instruction answered in the same chat. Today it is refused.
**Why P0:** Main is where most users live. **Independent test:** redirect frame on a Main session with a running turn; one new turn replies to `webchat` / session id.

Acceptance:
1. **Given** a Main chat with a running turn, **When** the redirect is sent from the web, **Then** the turn stops and exactly one new turn answers in the same web chat.
2. **Given** a scheduled, task or verifier chat with a running turn, **When** the redirect is sent from the web, **Then** the same happens.
3. **Given** a Main chat reached through a bound connector, with a running turn started from that connector chat, **When** `/stop-redirect` is typed in that connector chat, **Then** the instruction becomes a captured request of that chat and the agent's answer to it goes to that connector chat only; a plain answer still reaches no connector.
4. **Given** a connector conversation with a known peer id, **When** the redirect is sent from its web view, **Then** the same session continues, replies go to its connector chat, and the instruction and reply are in that session's transcript (today's behaviour).
5. **Given** a connector conversation whose peer id is missing, **When** the redirect is sent from the web, **Then** it continues in its own web chat (no refusal; ADR D2(b)).

### US-2 (P0) One rule decides "outside address or web chat"
Every place that decides where a session's replies, wakes and reports go uses the same rule.
**Independent test:** the rule's truth table, then each of the four call sites fed the same rows.

Acceptance:
1. **Given** any session, **When** its outside reply address is asked for, **Then** one exists only for type `channel` with a non-empty peer id and a place other than `""`/`"webchat"`.
2. **Given** a woken session, **When** its stream address is resolved, **Then** it follows the rule.
3. **Given** a delegation launched from a session, **When** the report-back target is set, **Then** it follows the rule.
4. **Given** stored sessions at boot, **When** the connector index is rebuilt, **Then** only sessions with an outside address are indexed.

### US-3 (P1) `channel` means place only
New kind-only sessions store an empty place; the product reads stored values as they are; the contract says what `channel` means.

Acceptance:
1. **Given** a new Main, scheduled, verifier or engine-created task session, **When** it is stored, **Then** its place is `""`.
2. **Given** connector, web-created and delegate sessions, **When** created, **Then** their places are unchanged.
3. **Given** the session list, **When** fetched, **Then** each session's `channel` is its stored place.
4. **Given** both `Session.yaml` copies, **When** compared, **Then** they are identical and describe `channel` as place only.

### US-4 (P1) Old labels are cleared once on the live instance
The Mac squad runs a one-time script on the live instance (and the Fly test gateway if it holds data) that clears the five legacy labels, with a dry run, a backup, a count report, a defined partial-failure outcome, and an offline restore path for use before the instance runs again.
**Independent test:** run the script against a temp home seeded with DS-2; check the report, the files and the backup.

Acceptance:
1. **Given** a home with legacy-labelled sessions, **When** the script runs without `--apply`, **Then** it reports what it would change and writes nothing.
2. **Given** the same home, **When** it runs with `--apply`, **Then** it backs up every file it will change first, then clears exactly the legacy labels on non-connector sessions, and reports counts.
3. **Given** a home already processed, **When** it runs again, **Then** it selects and changes zero files; its exit code still reports any unparseable file that is still there.
4. **Given** a backup and an instance that has not run since the script, **When** the backup is restored, **Then** every changed file is byte-identical to its state before the script.
5. **Given** an instance with stored sessions, **When** the build is rolled out, **Then** the script has run on that instance's data before the new build starts.
6. **Given** an `--apply` run where a rewrite fails after the backups are complete, **When** it stops, **Then** it exits 3, lists the changed and failed session ids, keeps the backups, and tells the operator not to start the instance; a re-run after fixing the cause finishes the job.

### US-5 (P1) Nothing else changes
Plain mid-turn messages still queue; a wake never writes session metadata; the previous build still routes correctly if rolled back, without restoring data.

Acceptance (existing checks, no new test framework):
1. **Given** a Main chat with a running turn, **When** a plain message arrives, **Then** it queues and cancels nothing (Scenario 21; HO-5).
2. **Given** a woken session, **When** the wake runs, **Then** its stored `channel` is not written (Scenario 10; existing `TestWakeSession_*`, `TestWakeChannel_*`).
3. **Given** the new build has run on processed data, **When** the previous build is installed without restoring any backup, **Then** cleaned and newly created sessions route to their web chat and later changes survive (Scenario 22; HO-9).

## Edge Cases

| Case | Expected |
|---|---|
| Connector session with empty peer id | No outside address; web redirect continues in its web chat (ADR D2(b); replaces today's refusal) |
| Connector-type session with place `webchat` and a peer id | No outside address; web chat with session id (ADR D2 corner; today streams to the peer) |
| Non-`channel` type with a real place and a peer id (constructed only; no writer exists) | No outside address |
| Place or peer id that is whitespace only | Empty (matches today's trimming in `ordinaryRedirectRoute`) |
| Legacy-labelled session before the script ran | Routes to its web chat (rule keys on `type`); API shows the stored label |
| Script: label with other case or spaces (`"Main"`, `" main "`), or not in the list | Not changed (exact, case-sensitive match) |
| Script: type `channel` session with place `main` | Not changed (connector sessions are never touched) |
| Script: unparseable `meta.json` | Not changed; listed by session id in the report; exit 1. Rollout stops there: the instance cannot boot on either build while that file is present (FR-026) |
| Script: directory without `meta.json`, or a file (not a directory) under `sessions/` | Skipped silently, not counted as a session |
| Script: home or `sessions/` missing | Exit 2, nothing written |
| Script: `--apply` with a backup folder that exists and is not empty | Exit 2, nothing written |
| Script: backup copy fails | Exit 2, no session identity file changed; copies already made stay in the backup folder, which has no `manifest.txt` and is never a restore source (FR-022) |
| Script: a rewrite fails after all backups succeeded | Stops at that file; exit 3; changed ids, failed id and not-attempted count printed; backups kept; "do not start the instance" (FR-030) |
| Script: second run with the same corrupt file still present | Zero selected; exit 1 again (FR-025) |
| Two connector sessions with the same `instance/peer` key at boot | Newest `CreatedAt` wins; equal times: greater id wins (today's order, FR-005) |
| Main redirect typed in a connector whose instance is unbound, or rebound to another pair, between finding the Main and admitting the instruction | Refused inside admission before any write: no capture, no transcript entry, no other Main touched or created, no turn; the not-applied notice (raw instruction, curated cause) goes to the chat that typed it (FR-028 (a)) |
| Main connector redirect captured, but its turn cannot start (shutdown closed intake, or routing refused) | The request is withdrawn (discarded) and cannot be answered; the instruction stays in the transcript; not-applied notice. If withdrawal fails: error log naming session and request id, and the notice says an answer may still arrive in this chat (FR-028 (a3)) |
| Main connector redirect: the continued turn answers through `answer_request` and then writes closing text | One addressed outbound; the closing text is not sent and causes no refusal (FR-028 (c)) |
| Main connector redirect: the continued turn answers in plain text only | Refused once, visibly, as for every Main connector turn today; nothing reaches any connector |
| Idle Main redirect | Exactly one turn runs, in the web chat |
| Unknown session id; empty instruction | Refused as today |

## Behavioral Contract

- When a session is type `channel`, has a non-empty peer id and a place other than `""`/`"webchat"`, the system uses (place, peer id, instance id) as its outside reply address.
- When any other session needs a reply address, the system uses its own web chat (`"webchat"`, session id).
- When a redirect states an origin channel other than `""`/`"web"`, the system routes the continuation to that channel and chat id.
- When that continuation is on a Main session, the system admits the instruction as a captured request of that connector chat, only if the instance is still bound to that same Main at the moment of admission; the agent's answer to that request goes to that chat; a plain answer reaches no connector (today's Main rule). If the turn then cannot start, the request is withdrawn and the chat is told.
- When a redirect from the web hits a connector conversation with an outside address, the system continues that session on its connector chat (today's behaviour).
- When a Main, scheduled, verifier or engine task session is created, the system stores place `""`.
- When a stored session is read, the system returns its stored place unchanged.
- When the one-time script runs without `--apply`, it writes nothing; with `--apply`, it backs up first, then clears only the five legacy labels on non-connector sessions; a second run selects nothing; a rewrite failure stops it with exit 3.
- When the previous build is rolled back after the new build has run, the system keeps the current data; backups are restored only before the instance has run again.

## Explicit Non-Behaviors

- The product must not clean, translate or rewrite legacy labels on read or on save, because the founder chose a one-time script (no installations in the wild).
- The product must not run any migration or upgrade step at boot; the script is run by hand and is not called by product code.
- The system must not add a new field (`is_main` or similar), enum or registry, because `type` already carries the kind (ADR D1).
- The system must not change how a redirect from the web view of a connector conversation is routed (founder Q2).
- The system must not change how plain mid-turn messages queue or cancel.
- The system must not change frontend logic; at most the comment at `src/lib/api/sessions.ts:64-65` and the regenerated comments under `src/lib/api/generated/`.
- The system must not change the Main default-send refusal (`publishResponseIfNeeded` → `refuseMainConnectorDefaultSend`) or its tests, must not pick "the latest connector sender" for a Main answer, and must not change `pkg/channels` or the channel-control interface.
- The system must not change the bus message channel `"system"` (`constants.IsInternalChannel`, `connector_egress.go:40`); it is not the stored place.
- The system must not change the contract's shape: `channel` stays a required free string, no enum.
- The system must not change where delegation results go for a Main turn that came from a connector (ADR G2); HO-6 only observes it.
- The script must not touch any file other than `<home>/sessions/<id>/meta.json` and its own backup folder, and must not change any field other than `channel`.

## Integration Boundaries

| System | In / out | Contract | Failure behaviour | Dev approach |
|---|---|---|---|---|
| Session store on disk | read / write `channel` | `meta.json` identity file; free string | Unreadable meta: existing redirect error, unchanged | Real store in temp dir |
| REST `GET /api/v1/sessions` | out `channel` | `Session.yaml` | Unchanged | Real handler in test gateway |
| WebSocket redirect frame | in | `RedirectFrame`; error frame on refusal | Unchanged | Real WS harness (`websocket_redirect_streaming_test.go` pattern) |
| Connectors (Telegram etc.) | in command, out reply | bus messages; Main answers only through a captured request (`Return` route) | Admission refusal → existing not-applied notice with curated text; plain Main answer → existing refusal | Bus + `boundConnFixture` in `pkg/agent` tests; real Telegram only in HO-2, HO-4, HO-6 |
| Connector request ledger | write one capture per Main connector redirect; discard it if the turn cannot start | `addressing.Capture` (existing) | Ledger unavailable → not applied, notice; discard failure → error log + notice wording of FR-028 (a3) | Real ledger in the `boundConnFixture` temp store |
| Live Mac instance / Fly test gateway data | script reads and rewrites `meta.json` | Files under `<home>/sessions` | Backup first; partial failure exit 3 then re-run; restore by copy only before the instance runs again | Temp home in script tests; real instance in HO-10 |

## Functional Requirements

### Routing and the shared rule
- **FR-001** `pkg/session` MUST provide `func (m *UnifiedMeta) OutsideReplyAddress() (channel, chatID, instanceID string, ok bool)`. `ok` is true only when `Type == channel` AND trimmed `PeerID != ""` AND trimmed `Channel` is neither `""` nor `"webchat"`; it then returns the trimmed `Channel`, `PeerID`, `InstanceID`. Otherwise `ok = false` and empty strings. A nil receiver returns `ok = false`.
- **FR-002** A redirect that states no origin (`""` or `"web"`) MUST route to the session's FR-001 address when one exists, else to `("webchat", session id)`. It MUST NOT refuse because the place has no peer id.
- **FR-003** A woken session's stream address MUST be the FR-001 (place, peer id) when one exists, else `("webchat", session id)`.
- **FR-004** A delegation's report-back target MUST be the steering session's FR-001 (place, peer id) when one exists, else `("webchat", steering session id)`; nil steering metadata gives the self target.
- **FR-005** In `rebuildChannelSessionIndex` only the eligibility test changes: a stored session is a candidate exactly when it has an FR-001 address. Everything else stays as on the release (`loop_session.go:55-69`): candidates are visited newest `CreatedAt` first, equal `CreatedAt` by descending session id; the key is `instanceID/peerID` with the instance id falling back to the place when empty; the first candidate for a key wins (`LoadOrStore`). No new index or registry.
- **FR-006** A redirect that states an origin channel other than `""`/`"web"` MUST route its continuation to that channel with the stated chat id (session id when none), for every kind including Main (route selection unchanged, `stop_redirect_root.go:111-117`). For a Main, delivery of the answer is governed by FR-028.
- **FR-028** (Main redirect typed in a connector; reuses the existing captured-request mechanism.) When the continuation built by FR-006 is on a Main session M and its channel is a connector (the existing test `connector_egress.go::AgentLoop.mainConnectorTurn(msg.Channel, msg.SessionID)`), `stop_redirect_root.go::continueOrdinaryAfterStop` MUST, after the running turn has stopped, as a sibling of its webchat transcript branch, admit the instruction through the existing bound-connector admission with M pinned (a), given a copy of the continuation with `SessionID` cleared (admission accepts only messages without one, `connector_intake.go::boundConnectorPair`) and its `Metadata` map cloned (admission writes into it). Admission then does what it does for every bound connector message: binding and eligibility from config only, size gates, one capture whose return route is (instance = the channel the control named, chat = the stated chat id, owner = the instance's bound pair) and whose sender is the sender id the channel adapter passed after its allow check, the transcript entry under the request id, and the model envelope with that request id. The admitted message (envelope content, `TranscriptEntryID`, `SessionID`) is the one the new turn then runs with, through the existing `resolveMessageRoute` / `revive_support.go::runRevivedOrdinaryTurn`, so the existing turn admission marks the request delivered (`runRevivedOrdinaryTurn` → `loop_inbound_dispatch.go::processMessage` → `revive_inbound.go::runInboundTurnWithRevival` → `markRequestDelivered`) and the channel transcript write is skipped because `TranscriptEntryID` is set (`loop_inbound_dispatch.go::processMessage`) (🔶 code reading of both; T-08 asserts exactly one transcript entry for the instruction). Then:
  - (a) **The expected Main is pinned inside the admission that writes (grill R2-B1).** The body of `connector_intake.go::admitBoundConnectorInputCtx` moves unchanged into a sibling `admitBoundConnectorInputForMain(ctx, msg, wantMainID string)`; `admitBoundConnectorInputCtx` becomes a one-line call with `wantMainID = ""`, so `loop.go::admitAndDispatch` and every existing test keep today's behaviour exactly. When `wantMainID != ""`, right after the function's own single `boundConnectorPair` call (one config snapshot) and before eligibility, transcription, the Main lookup or creation, the capture, the transcript entry or any other write, it MUST return the existing curated refusal ("This conversation can't be handled right now.") with a diagnostic error when the instance is not bound (never the silent no-op `"", nil`), or when `session.MainSessionID(pair.WorkspaceID, pair.AgentID)` fails or differs from `wantMainID`. All later steps keep using that same pair, as today. The FR-028 branch passes `wantMainID = M` and treats admission as successful ONLY when the refusal is empty, the error is nil, the admitted copy's `SessionID == M` and its `TranscriptEntryID` is non-empty; anything else is a refusal. There is no separate pre-check before admission (the round-1 preliminary `boundConnectorPair` call is removed: it read a different snapshot from the one admission uses). A rebind after admission is handled as for every bound message: the capture names the old owner, and `pkg/channels/return_route.go::checkReturnRoute` refuses the answer at the final send.
  - (a2) **Refused admission.** No turn runs and the not-applied notice (a4) is sent. Refusals under (a), eligibility and size gates come before any write. For a failed transcript append admission keeps its existing best-effort discard (it logs a failed discard, `connector_intake.go::admitBoundConnectorInputCtx` step 5); this spec claims no more than that.
  - (a3) **Captured, but the turn cannot start (grill R2-M1).** If admission succeeded and then `resolveMessageRoute` returns an error or `runRevivedOrdinaryTurn` returns false (shutdown closed intake; nothing ran), the branch MUST withdraw the request before the notice, with the mechanisms admission already uses after a failed append: `AgentLoop.settleRequest(id)` and `RequestLedger().Discard(M, id)`, id = the admitted `TranscriptEntryID`. The instruction's transcript entry stays (history; the notice says it was not applied). After a successful withdrawal, answering that id is refused (`addressing.Ledger.Resolve` returns `ErrDiscarded`) and sends nothing; a re-sent redirect is a new request with a new id. If `Discard` fails, the branch logs an error naming the session and request id, and the notice's cause says the instruction could not be withdrawn and an answer may still arrive in this chat (the request's captured route is this same chat, so no other chat can receive it).
  - (a4) **Notice content (grill R2-N1).** Every not-applied notice of this branch MUST be built from the original continuation (the raw instruction as typed, its channel and chat), never from the admitted copy, whose `Content` is the model envelope. The cause passed to `stop_redirect_root.go::reportUndeliveredOrdinaryRedirect` MUST be curated text only: admission's refusal text, "the instruction could not be started", or that text plus "and could not be withdrawn; an answer may still arrive here". The diagnostic error (paths, pair ids, store errors) is logged separately and never put in the notice, as `loop.go::admitAndDispatch` already does for normal intake. The notice stays a plain outbound to the chat that typed the redirect (today's notice behaviour), never to another sender.
  - (b) The answer reaches the connector only through the existing answer path (`answer_request` / `send_message reply_to` → `AddressRouter.Reply` → `replyViaConnector`, whose `Return` route is checked again at the final send).
  - (c) **Two outcomes, the Main default-send refusal unchanged (grill R2-M3).** (c1) *Successful addressed answer:* when the continued turn calls the real `answer_request` tool (or `send_message reply_to`) with the request id, exactly one outbound goes to the captured chat with `Return.RequestID` = that id; closing text of the same round is not published and raises no refusal, because `loop_publish.go::publishResponseIfNeeded` returns on the message tool's sent marker before the Main check (`tools/message_address.go::MessageTool.executeReplyFormWithMedia` sets it; `answer_request` uses the same `MessageTool`, `loop_wire.go::registerCoreTools`). (c2) *Plain-only answer:* with no successful addressed send in the round, nothing reaches any connector and the existing refusal happens exactly once (error frame + refusal note; `TestMainConnectorDefaultSend_RefusedOnceWithNote` unchanged).
  - (d) No change to `pkg/channels`, to `RequestRedirectByChannelChat`'s signature, or to redirect route selection. Accepted limits of reusing the control's arguments: the capture has no platform message id (the answer is not threaded as a reply) and no display name (the sender label reads "a sender on <instance>").
  - (e) Non-Main sessions (per-chat connector sessions of unbound instances) keep today's continuation exactly.
- **FR-007** A redirect from the web on a connector conversation with an FR-001 address MUST continue that same session; the new turn's reply and any not-applied notice MUST go to its connector chat (place, peer id), and the instruction and reply MUST be recorded in that session's transcript (unchanged; founder Q2).
- **FR-008** For a redirect routed to the web chat, the system MUST stop the running turn (if any), record the instruction as the chat's next user message, run exactly one new turn with it, and send the reply to `("webchat", session id)`. The web redirect frame MUST receive no error frame.

### Stored place
- **FR-009** New sessions created by `GetOrCreateMainSession`, `NewScheduledSession`, `GetOrCreateScheduledSession`, `NewVerifierSession`, the task executor and the plan engine MUST store `channel = ""`.
- **FR-010** `NewChannelSession` MUST keep storing its connector place; web/REST-created chats `"webchat"`; delegates `""`.
- **FR-011** Reading a stored session MUST return its stored `channel` unchanged (no clean-up, no rewrite on read or save).
- **FR-013** Waking a session MUST NOT change its stored `channel` (unchanged; existing PW tests).
- **FR-014** `GET /api/v1/sessions` MUST return each session's stored place: `""` for Main created after this change; the connector place for connector sessions; `"webchat"` for web-created chats.
- **FR-015** Both `Session.yaml` copies MUST stay byte-identical; `channel` MUST stay required, `type: string`, no `enum`; its description MUST say: where the conversation lives — the connector channel of a `type: channel` session (e.g. "telegram"), "webchat" for chats created in the web app, empty for sessions not bound to one place (main, scheduled, verifier, engine-created task, delegate); it does not say what kind the session is: use `type`. Generated files under `src/lib/api/generated/` and `pkg/api/generated/` are regenerated; their diff MUST be comment-only (today's text at `src/lib/api/generated/openapi-types.ts:4847` and `pkg/api/generated/openapi_types.gen.go:26177,26409`; no embedded copy of the spec was found in either folder).
- **FR-016** A plain message sent while a turn runs MUST be queued and MUST NOT cancel or stop the turn (unchanged).
- **FR-017** The previous build MUST still route a session stored with `channel: ""` and no peer id to its web chat, with no data restored (rollback safety). Holds by reading the release at the four sites: `stop_redirect_root.go:119-121`, `wake_address.go:9`, `steer_launcher.go:658`, `loop_session.go:62` (🔶 code reading); proven hands-on by HO-9 (Scenario 22) against a script-cleaned legacy session and a session created by the new build.
- **FR-018** `docs/internal/specs/parent-wake-visibility-spec.md` MUST carry a dated correction to PW-D1 and to the scenario "A web wake does not rewrite the stored channel": Main stores no channel; old stored labels were cleared once by the #1931 one-time script, the one sanctioned rewrite; a wake never rewrites metadata. The PW-D1 rule "the woken turn streams in the open web chat" stays.
- **FR-031** `docs/using-omnipus-ui.md`, section "Stop and redirect commands" (lines 341-357), MUST gain a short paragraph (a few sentences, no new page) saying: `/stop-redirect` typed in the web app in a Main, scheduled, task or verifier chat is answered in that web chat; typed in the web view of a connector conversation (for example Telegram) the conversation continues and its answer goes to that connector chat; a connector conversation with no recorded chat address continues in its web chat; typed in a connector chat that reaches the agent's main conversation, it is answered in that connector chat as the agent's answer to that message (a plain reply is not sent there, as for any message in a main conversation). The docs reviewer checks the final wording against FR-002, FR-007 and FR-028.

### One-time script (founder Q1)
- **FR-019** The script MUST live at `scripts/oneoff-1931-clear-session-kind-labels.py`, use Python 3 standard library only, and have its test at `scripts/oneoff-1931-clear-session-kind-labels.test.py` (`unittest`, run with `python3 -I`, same convention as `scripts/ci-quarantine.test.py`). Product code MUST NOT call it. Its header MUST contain the runbook (FR-026, FR-027, FR-029, FR-030).
- **FR-020** Usage: `python3 -I scripts/oneoff-1931-clear-session-kind-labels.py --home <home> [--apply --backup-dir <dir>]`. It scans every `<home>/sessions/<name>/meta.json` one level deep. Without `--apply` it is a dry run and MUST write nothing (no session file, no backup). `--apply` without `--backup-dir` MUST be refused (exit 2).
- **FR-021** A file is selected only when it parses as a JSON object, its `type` is not `"channel"`, and its `channel` exactly equals one of `main`, `scheduled`, `verifier`, `system`, `heartbeat`. For a selected file the script MUST set `channel` to `""` and keep every other key, value and key order. Unselected files MUST stay byte-identical.
- **FR-022** The script MUST visit sessions in ascending `<name>` order (byte order). In `--apply` mode the script MUST, before changing any file, copy every selected file to `<backup-dir>/sessions/<name>/meta.json` (byte-identical, same mode) and then, LAST, write `<backup-dir>/manifest.txt` listing each session id with its old label. The manifest marks a complete backup set: a backup folder without `manifest.txt` is incomplete and MUST NOT be used for any restore. If `<backup-dir>` exists and is not empty, or any backup copy or the manifest write fails, it MUST exit 2 and change no session identity file; copies already made are left as they are (no clean-up step), and the operator uses a new empty folder for the next attempt. When no file is selected it writes nothing, not even the manifest.
- **FR-023** Each rewrite MUST be atomic (temporary file in the same directory, then rename), keep the original file mode, and write JSON indented with two spaces followed by a newline, non-ASCII kept as UTF-8.
- **FR-024** The script MUST print one summary: files scanned, connector sessions skipped, per-label counts selected (dry run: "would change"; apply: "changed"), and unparseable files by session id. Exit codes: 0 = done, no unparseable files; 1 = done but at least one unparseable file (left untouched); 2 = refused, no session identity file changed (missing home or `sessions/`, bad arguments, non-empty backup folder: nothing written at all; backup failure: an incomplete backup folder without `manifest.txt` may remain, FR-022); 3 = apply failed part-way (FR-030). The script MUST expose `main(argv) -> int` and call it only under `if __name__ == "__main__"`, so its test can load it with `importlib` and inject a failure.
- **FR-025** Idempotency means: a second run on the same home MUST select zero files and change nothing, whatever the parse status. Its exit code follows FR-024: 1 if the same unparseable files are still there, 0 if there are none.
- **FR-026** Rollout order MUST be, on the live Mac instance and on the Fly test gateway if it holds sessions: (1) stop the instance; (2) dry run, save output; (3) `--apply` with a new backup folder outside `<home>/sessions`, save output; (4) dry run again, save output; (5) install the new build; (6) start. Step 5 MAY start only when step 4 reports 0 "would change" AND exits 0. Exit 1 at step 2, 3 or 4 (an unparseable session identity file) is NOT permission to start: the instance cannot boot with that file on either build (boot conversion refuses present unparseable metadata: `pkg/session/unified_conv.go::convNormalizeSavedChatDirs`, propagated by `pkg/agent/loop_construct.go::initializeConvertedSessions` and `pkg/agent/loop.go::NewAgentLoop`; pinned by `pkg/agent/conv_boot_step_test.go::TestConvBootStep_RefusesOnCorruptSavedChat`). On exit 1: stop, keep the file's bytes as they are (copy it aside), record the session ids in the run note and tell the chief; the chief decides with the data owner how that file is repaired or removed, outside this script and this spec (the script never edits or deletes it; the product's boot check stays as it is). Then run step 4 again and continue only on 0 "would change" AND exit 0. Exit 2 at any step, exit 3, or any other step-4 result: stop, do not start the new build, follow FR-030 or FR-027. A zero change count alone is never enough to continue. The new build MUST NOT start on an instance whose data the script has not processed. The script MUST NOT run while the instance runs (a running build saves its in-memory value on the next write of that session and would undo the change).
- **FR-027** Offline script recovery, only while the instance has not run since the script (between FR-026 steps 3 and 6, or after an exit 3 the operator decides not to finish): restore from the FIRST COMPLETE backup folder of this rollout, i.e. the first one holding `manifest.txt` (`cp -Rp <backup-dir>/sessions/. <home>/sessions/`), install or keep the previous build, start. Every file the script changed is then byte-identical to its pre-script state. This restore MUST NOT be used once any build has run on the processed data (FR-029).
- **FR-029** Binary rollback after the new build has run MUST be: stop the instance; install the previous build; start. No backup is restored and no label is put back: the previous build routes `channel: ""` to the web chat (FR-017), and restoring whole identity files would lose later changes (title, model, effort, partitions, owner, `unified_meta_files.go:66-94`) and could recreate deleted sessions. Sessions the previous build creates after rollback store its old labels again; that is the previous build's own behaviour. Rolling forward again repeats FR-026 (the script selects only those).
- **FR-030** If a rewrite fails in `--apply` after all backups succeeded (temporary file create, write, mode change or rename), the script MUST stop at that file, remove its temporary file when present, leave that file and every later selected file unchanged, keep the backup folder, print the changed session ids, the failed session id with the cause, and the number not attempted, print "Do not start the instance: fix the cause, then re-run --apply with a new empty backup folder", and exit 3. Recovery is the idempotent re-run: fix the cause, run `--apply --backup-dir <new empty folder>`; files already changed are not selected again; keep both backup folders. To abandon instead, use FR-027 with the first complete backup folder, which holds the original of every selected file.

### Success Criteria
- **SC-001** All RED tests fail on `origin/release/v0.1.1` and pass after the fix (`heavy go test -p 1 -tags goolm,stdjson -run '<name>' ./pkg/<pkg>/`; `python3 -I scripts/oneoff-1931-clear-session-kind-labels.test.py`).
- **SC-002** All "must pass unchanged" tests pass with no edits.
- **SC-003** Under `src/` the fix's diff changes only (a) the comment at `src/lib/api/sessions.ts:64-65` and (b) comment lines in regenerated files under `src/lib/api/generated/`. Check: `git diff origin/release/v0.1.1 -- src/ ':!src/lib/api/generated/' ':!src/lib/api/sessions.ts'` is empty; in `git diff -U0 origin/release/v0.1.1 -- src/lib/api/generated/ src/lib/api/sessions.ts`, every added or removed line (`^[+-]`, excluding the `+++`/`---` headers) is a comment line (after leading spaces it starts with `//`, `*` or `/*`). No frontend logic or type shape changes.
- **SC-004** `git grep -nE 'NewSession\([^)]*"(main|scheduled|verifier|system|heartbeat)"|Channel: *"main"' -- pkg/session/unified.go pkg/agent/task_executor.go pkg/agent/plan_engine.go` returns nothing.
- **SC-005** No read-time or save-time clean-up is added: `git diff origin/release/v0.1.1 -- pkg/session/unified_meta_files.go` is empty, the bodies of `readUnifiedMeta` and `u5WriteIdentityLocked` are unchanged, and `git diff -U0 origin/release/v0.1.1 -- pkg/session/unified.go | grep -E '^\+[^+]' | grep -E '"(main|scheduled|verifier|system|heartbeat)"'` returns nothing (only ADDED lines are searched; the removed creator lines must and will appear).
- **SC-006** The diff of both `Session.yaml` copies changes only `description` text; in `git diff -U0 origin/release/v0.1.1 -- pkg/api/generated/` every added or removed line is a `//` comment.
- **SC-007** HO-1..HO-10 pass, run by two testers on different models from the developer and from each other; HO-10 output (dry run, apply, second dry run) saved for the live instance.
- **SC-008** The diff of `pkg/agent/loop_publish.go`, `pkg/agent/connector_egress.go`, `pkg/agent/address_router.go` and `pkg/channels/` is empty (FR-028 reuses them; the default-send refusal and the final return-route check are unchanged). The diff of `pkg/agent/connector_intake.go` is limited to FR-028 (a): the body of `admitBoundConnectorInputCtx` moved into `admitBoundConnectorInputForMain`, the expected-Main check between its binding step and its eligibility step, and `admitBoundConnectorInputCtx` calling it with `""`; every test in `pkg/agent/connector_intake_test.go` passes unedited (reviewer checks the diff; no second copy of the intake logic).

## BDD Scenarios

### Scenario 1 (issue #1931 reproduction): Redirect from the web in a Main chat stored with channel `main`
**Traces to:** US-1 AS-1 · **Category:** Happy Path (bug reproduction)
- **Given** a Main session (type `main`) whose stored `meta.json` has `channel: "main"` and no peer id, as Team B read on two instances
- **And** a turn is running in it
- **When** the web app sends the redirect frame for `/stop-redirect Summarise only the risks.` (channel `"web"`, no chat id)
- **Then** the server sends no error frame (the web shows no "Not stopped. Press Stop again.") and no error contains "no recorded chat id"
- **And** the running turn stops
- **And** the transcript's next user message is "Summarise only the risks."
- **And** exactly one new provider call runs, and its reply goes to channel `webchat`, chat id = the Main session id

### Scenario 2: Same redirect in a freshly created Main
**Traces to:** US-1 AS-1 · **Category:** Happy Path
- **Given** a Main created by `GetOrCreateMainSession` after the fix, with a running turn
- **When** the web redirect frame is sent
- **Then** the outcome equals Scenario 1

### Scenario Outline 3: Redirect from the web in other kind-only chats
**Traces to:** US-1 AS-2 · **Category:** Happy Path
- **Given** a `<type>` session stored with place `<place>`, no peer id, a running turn
- **When** the web redirect is sent
- **Then** exactly one new turn answers in `("webchat", session id)`, no refusal

| type | place |
|---|---|
| scheduled | scheduled |
| scheduled | "" |
| task | system |
| task | "" |
| verifier | verifier |
| chat | webchat |

### Scenario 4: Idle Main chat redirect
**Traces to:** US-1 AS-1 · **Category:** Edge Case
- **Given** a Main chat with no running turn
- **When** the web redirect is sent
- **Then** exactly one turn runs with the instruction, replying in `("webchat", session id)`

### Scenario 5: Main redirect typed in a connector is answered in that connector through its captured request
**Traces to:** US-1 AS-3 · **Category:** Alternate Path (changed: today nothing is captured and the plain answer is refused)
- **Given** a connector instance `telegram.a` bound to the pair whose Main is M, and a running turn of M started by a captured request from chat `tg-chat-42`, sender `telegram:42`
- **When** `/stop-redirect Summarise only the risks.` arrives through the channel control from `telegram.a` / `tg-chat-42`, sender `telegram:42`
- **Then** the running turn stops and exactly one new turn of M runs
- **And** M's ledger holds one new capture for the instruction: source connector, instance `telegram.a`, chat `tg-chat-42`, owner = the bound pair, sender `telegram:42`
- **And** M's transcript holds the instruction under that request id, and the new turn's model input carries that request id
- **When** the new turn calls the real `answer_request` tool with that request id and then ends with closing text
- **Then** exactly one outbound message is published, to `telegram.a` / `tg-chat-42`, with a `Return` route naming that request
- **And** the closing text is not published anywhere and no "Not sent to" error frame appears (the round already sent its addressed answer, FR-028 (c1))

### Scenario 5d: A plain-only answer of a Main connector redirect is refused as today
**Traces to:** US-1 AS-3 · **Category:** Error Path (regression of today's Main rule)
- **Given** Scenario 5's setup, up to the new turn running with the captured request
- **When** the new turn answers with plain text only (no answer tool call)
- **Then** nothing is published to any connector, and exactly one "Not sent to telegram.a" refusal happens (one error frame, one refusal note) (FR-028 (c2))

### Scenario 5b: A Main connector redirect cannot answer another sender's chat
**Traces to:** US-1 AS-3 · **Category:** Error Path (security regression)
- **Given** Scenario 5's setup plus an earlier captured request of M from chat `tg-chat-77`, sender `telegram:77` (two distinct chats and senders)
- **When** the redirect from `tg-chat-42` is applied and the new turn answers the redirect's request id with the real `answer_request` tool
- **Then** the only outbound message goes to `tg-chat-42`; nothing goes to `tg-chat-77`
- **And** answering a request id that M's ledger does not hold is refused and sends nothing

### Scenario Outline 5c: The binding changes between finding the Main and admitting the instruction
**Traces to:** US-1 AS-3 · **Category:** Error Path (security, grill R2-B1)
- **Given** a bound Main M of pair A on `telegram.a`, the redirect from `telegram.a` / `tg-chat-42` already routed to M, and then the config `<change>` before the continuation admits the instruction
- **When** the continuation runs
- **Then** no capture and no transcript entry for the instruction exist in M or in N, N is not created when it did not exist, and no provider call runs
- **And** the not-applied notice is published to `telegram.a` / `tg-chat-42`, quotes the raw instruction (not the model envelope), and contains neither `connector intake` nor any workspace or agent id of pair A or B (FR-028 (a4))

| change |
|---|
| removes `telegram.a`'s binding (unbound) |
| rebinds `telegram.a` to an eligible pair B whose Main N does not exist yet |
| rebinds `telegram.a` to an eligible pair B whose Main N already exists |

### Scenario 5e: A captured redirect whose turn cannot start is withdrawn
**Traces to:** US-1 AS-3 · **Category:** Error Path (grill R2-M1)
- **Given** a bound Main M of pair A on `telegram.a` with no running turn, and shutdown has closed new-turn intake
- **When** the redirect continuation for `telegram.a` / `tg-chat-42` runs
- **Then** exactly one capture for the instruction exists in M's ledger and it is discarded; answering its id through the real router is refused and publishes nothing
- **And** M's transcript holds the instruction under that id; no provider call runs
- **And** the not-applied notice goes to `telegram.a` / `tg-chat-42` and quotes the raw instruction, not the envelope (no `request_id=`)
- **And** a second admission of the same instruction for M gets a different request id that resolves, while the first id stays refused
- **Given** instead that M's ledger already holds one newline-terminated unreadable line (so the capture is appended but the discard cannot read the ledger)
- **Then** an error is logged naming M and the request id, and the notice says the instruction could not be withdrawn and an answer may still arrive in this chat

### Scenario 6: Connector conversation redirected from its web view keeps today's route (founder Q2)
**Traces to:** US-1 AS-4 · **Category:** Alternate Path (regression)
- **Given** a type `channel` session on `telegram` with peer id `r2Chat` and a running turn
- **When** the web redirect is sent (channel `"web"`, no chat id)
- **Then** the same session id continues (no new session)
- **And** the new turn's reply goes to `telegram` / `r2Chat`
- **And** the session's transcript holds the instruction as a user message and the reply as the next assistant message (what the web view shows)
- **And** when the stop cannot land, the not-applied notice goes to `telegram` / `r2Chat`

### Scenario 7: Connector conversation without peer id redirected from the web
**Traces to:** US-1 AS-5 · **Category:** Error Path (changed behaviour, ADR D2(b))
- **Given** a type `channel` session on `telegram` with an empty peer id and a running turn
- **When** the web redirect is sent
- **Then** it is accepted, the turn stops, one new turn replies to `("webchat", session id)`

### Scenario 8: Redirect errors stay as they are
**Traces to:** US-1 AS-1 · **Category:** Error Path (regression)
- **Given** an unknown session id, or a whitespace-only instruction on a Main with a running turn
- **When** the redirect is sent
- **Then** it is refused ("not found" / usage text) and no turn is stopped

### Scenario Outline 9: The outside-address rule
**Traces to:** US-2 AS-1 · **Category:** Happy / Edge
- **Given** a session with `<type>`, `<place>`, `<peer>`, `<instance>` (DS-1)
- **When** `OutsideReplyAddress()` is called
- **Then** it returns `<want>`

### Scenario Outline 10: Wake, report-back and index follow the rule
**Traces to:** US-2 AS-2, AS-3, AS-4 · **Category:** Happy / Edge
- **Given** a session from DS-1 (for the index also the DS-1b collision sets)
- **When** it is woken / a delegation is launched from it / the index is rebuilt at boot
- **Then** the stream address / report-back target / index membership equals the DS-1 result, and each DS-1b key maps to the DS-1b winner
- **And** after the wake, the woken session's stored `channel` reads back exactly as seeded (FR-013, US-5 AS-2; only the `channel` field is compared, other turn-updated metadata may change)

### Scenario Outline 11: Creators store an empty place
**Traces to:** US-3 AS-1 · **Category:** Happy Path
- **Given** an empty store
- **When** `<creator>` creates a session (DS-3 rows 1-7)
- **Then** stored `meta.json` and `GetMeta` both have `channel: ""`

### Scenario 12: Unchanged creators keep their place
**Traces to:** US-3 AS-2 · **Category:** Alternate Path (regression)
- **Given** an empty store
- **When** a connector session, a REST chat and a delegate are created (DS-3 rows 8-10)
- **Then** their places are `telegram`, `webchat`, `""`

### Scenario 13: Session list shows the stored place
**Traces to:** US-3 AS-3 · **Category:** Happy Path
- **Given** a new Main, an unscripted legacy Main (`"main"`), a telegram connector session and a web chat
- **When** `GET /api/v1/sessions` is called
- **Then** `channel` is `""`, `"main"`, `telegram`, `webchat` respectively (the product shows what is stored; only the script clears old labels), and every `type` is unchanged

### Scenario 14: Contract text
**Traces to:** US-3 AS-4 · **Category:** Happy Path
- **Given** both `Session.yaml` copies
- **When** read
- **Then** byte-identical, `channel` required, `type: string`, no `enum`, description mentions `webchat`, empty, and `type`

### Scenario 15: Script dry run writes nothing
**Traces to:** US-4 AS-1 · **Category:** Happy Path
- **Given** a temp home seeded with DS-2
- **When** the script runs without `--apply`
- **Then** it reports "would change" counts per DS-2, exits 0 (1 if row 14 is seeded), and every file under the home is byte-identical; no backup folder is created

### Scenario 16: Script apply backs up then clears exactly the legacy labels
**Traces to:** US-4 AS-2 · **Category:** Happy Path
- **Given** the DS-2 home and a backup folder that already exists and is empty (DS-5 row 7: an existing empty folder is accepted)
- **When** the script runs with `--apply --backup-dir <dir>`
- **Then** every DS-2 "changed" row now has `channel: ""` with all other keys, values and order kept, same file mode
- **And** every other row is byte-identical
- **And** the backup holds the pre-run bytes of exactly the changed files plus `manifest.txt`
- **And** the counts printed match DS-2, and the exit code is 1 because row 14 is present (the six changes are kept; exit 1 is not a refusal)

### Scenario Outline 17: Script second run selects nothing
**Traces to:** US-4 AS-3 · **Category:** Edge Case
- **Given** `<home>` already processed by `--apply`
- **When** the script runs again (dry run, then `--apply` with a new empty backup folder)
- **Then** both report 0 to change, change no session file, write nothing into the new backup folder (no copy, no manifest), and exit `<code>`

| home | code |
|---|---|
| DS-2 rows 1-16 (row 14 still corrupt) | 1, row 14 listed again |
| DS-2 rows 1-13, 15, 16 (no corrupt file) | 0 |

### Scenario 18: Offline restore before the instance runs again
**Traces to:** US-4 AS-4 · **Category:** Happy Path
- **Given** a home processed with `--apply`, its backup, and no build run since
- **When** `cp -Rp <backup-dir>/sessions/. <home>/sessions/` runs
- **Then** every session file is byte-identical to the seeded original

### Scenario Outline 19: Script refusals
**Traces to:** US-4 AS-2 · **Category:** Error Path
- **Given** `<setup>` (DS-5 rows 1-6 only; row 7 is an accepted apply and belongs to Scenario 16)
- **When** the script runs with `<args>`
- **Then** it exits `<code>` (2) and no session identity file changes

### Scenario 20: Rollout order on an instance with data
**Traces to:** US-4 AS-5 · **Category:** Happy Path (hands-on HO-10)
- **Given** an instance with legacy-labelled sessions running the previous build
- **When** the FR-026 steps run in order
- **Then** the second dry run reports 0 "would change" and exits 0, the new build starts, and `GET /api/v1/sessions` shows no legacy label on any non-connector session
- **And** if any dry run or the apply exits 1 (an unparseable identity file), the operator stops, does not start any build, and tells the chief; the rollout continues only after that file is resolved outside this spec and a new second dry run exits 0 (FR-026)

### Scenario 21: Plain mid-turn message still queues
**Traces to:** US-5 AS-1 · **Category:** Happy Path (regression, hands-on HO-5)
- **Given** a Main chat with a running turn
- **When** a plain message arrives from the web
- **Then** the turn is not stopped or cancelled and the message is not dropped

### Scenario 22: Binary rollback keeps current data
**Traces to:** US-5 AS-3 · **Category:** Happy Path (rollback, hands-on HO-9)
- **Given** an instance processed by FR-026 and then run on the new build, holding (a) a legacy Main the script cleaned to `channel: ""`, (b) a Main or scheduled chat created by the new build (`channel: ""`), and later changes made on the new build: a renamed chat title, a changed model or effort, and new messages in (a)
- **When** FR-029 runs (stop, install the previous build, start), with no backup restored
- **Then** `/stop-redirect` from the web on (a) and (b) answers in their web chats (no "Not stopped"), a woken parent streams in the open web chat, and nothing crashes
- **And** the title, model/effort and messages changed on the new build are still there, and no deleted session reappears

### Scenario 23: Apply fails part-way, then the re-run finishes
**Traces to:** US-4 AS-6 · **Category:** Error Path
- **Given** the DS-2 home without row 14 and an empty backup folder
- **When** `--apply` runs and the rewrite of the second selected session (by FR-022 order) fails after all backups were written
- **Then** at the moment the rewrite fails, the backup folder already holds every selected original and `manifest.txt`
- **And** it exits 3; the first selected file has `channel: ""`; the failed file and every later selected file are byte-identical to the seed; no temporary file is left; the backup folder holds every selected original and `manifest.txt`; the output names the changed id, the failed id, the count not attempted and "Do not start the instance"
- **When** the fault is removed and `--apply` runs with a new empty backup folder
- **Then** it exits 0, the remaining selected files now have `channel: ""`, the first-changed file is not selected again, and restoring the FIRST complete backup folder (FR-027) makes every file byte-identical to the seed

## Test Datasets

### DS-1: Outside-address rule (FR-001; T-01, T-10, T-11, T-12)

| # | type | place | peer | instance | Want | Category | Traces to |
|---|---|---|---|---|---|---|---|
| 1 | main | "" | "" | "" | none → web | Happy | Sc 2, 9 |
| 2 | main | main | "" | "" | none → web | Legacy (unscripted) | Sc 1, 9 |
| 3 | scheduled | scheduled | "" | "" | none → web | Legacy | Sc 3, 9 |
| 4 | scheduled | "" | "" | "" | none → web | Happy | Sc 9 |
| 5 | verifier | verifier | "" | "" | none → web | Legacy | Sc 9 |
| 6 | task | system | "" | "" | none → web | Legacy | Sc 9 |
| 7 | task | webchat | "" | "" | none → web | Happy | Sc 9 |
| 8 | heartbeat | heartbeat | "" | "" | none → web | Legacy | Sc 9 |
| 9 | delegate | "" | "" | "" | none → web | Happy | Sc 9 |
| 10 | chat | webchat | "" | "" | none → web | Happy | Sc 9 |
| 11 | chat | main | "" | "" | none → web | Legacy | Sc 9 |
| 12 | channel | telegram | peer-9 | telegram.1 | (telegram, peer-9, telegram.1) | Happy | Sc 6, 9 |
| 13 | channel | telegram | "" | telegram | none → web | Error (D2b) | Sc 7, 9 |
| 14 | channel | webchat | peer-web | webchat.1 | none → web | Edge (D2 corner) | Sc 9, 10 |
| 15 | channel | "" | peer-x | "" | none → web | Boundary | Sc 9 |
| 16 | main | telegram | peer-9 | telegram.1 | none → web | Edge (constructed) | Sc 9, 10 |
| 17 | channel | telegram | "   " | telegram | none → web | Boundary (whitespace) | Sc 9 |
| 18 | channel | "  telegram " | " peer-9 " | telegram.1 | (telegram, peer-9, telegram.1) | Boundary (trim) | Sc 9 |
| 19 | channel | telegram | peer-9 | "" | (telegram, peer-9, "") | Boundary | Sc 9, 10 |
| 20 | nil meta | | | | none | Error | Sc 9 |

"none → web" = `ok = false`; callers use `("webchat", session id)`. Rows 2, 3, 5, 6, 8, 11, 14, 16 are built by rewriting `meta.json` on disk and reopening the store (no creator makes them after the fix).

### DS-1b: Index key collisions (FR-005; T-12)

All rows are type `channel`, place `telegram`, peer `peer-9`, instance `telegram.1` (one key `telegram.1/peer-9`).

| # | Sessions seeded (id, `created_at`) | Want indexed for the key | Category |
|---|---|---|---|
| 1 | `s-old` 2026-11-01T10:00Z, `s-new` 2026-11-20T10:00Z | `s-new` (newest wins) | Collision |
| 2 | `s-aaa` and `s-bbb`, both 2026-11-20T10:00Z | `s-bbb` (equal time: greater id wins) | Collision tie-break |
| 3 | `s-new` 2026-11-20T10:00Z (with an FR-001 address) and `s-main` type `main` 2026-11-25T10:00Z with the same place/peer/instance (DS-1 row 16 shape) | `s-new` (the Main is not a candidate) | Eligibility before ordering |

### DS-2: Script selection (FR-021, FR-024; T-30..T-33)

| # | type | stored channel | Other content | Result | Category | Traces to |
|---|---|---|---|---|---|---|
| 1 | main | main | owner, workspace_id, agent_ids | changed → "" | Legacy | Sc 15, 16 |
| 2 | scheduled | scheduled | | changed | Legacy | Sc 16 |
| 3 | verifier | verifier | | changed | Legacy | Sc 16 |
| 4 | task | system | title with `漢字 & <tag>` | changed; title bytes-equal as UTF-8 value | Legacy + unicode | Sc 16 |
| 5 | heartbeat | heartbeat | | changed | Legacy | Sc 16 |
| 6 | chat | main | | changed | Legacy | Sc 16 |
| 7 | chat | webchat | | untouched | Happy | Sc 16 |
| 8 | channel | telegram | peer_id, instance_id | untouched (connector skipped) | Happy | Sc 16 |
| 9 | channel | main | | untouched (connector skipped) | Edge | Sc 16 |
| 10 | main | Main | | untouched | Edge (case) | Sc 16 |
| 11 | main | " main " | | untouched | Edge (exact) | Sc 16 |
| 12 | main | "" | | untouched | Boundary | Sc 16, 17 |
| 13 | scheduled | mainx | | untouched | Edge (not in list) | Sc 16 |
| 14 | (corrupt) | `{"type":"main","channel":` | | untouched; listed; exit 1 | Error | Sc 15, 16 |
| 15 | (dir without meta.json) | | | skipped, not counted | Boundary | Sc 16 |
| 16 | (plain file `sessions/README`) | | | skipped, not counted | Boundary | Sc 16 |
| 17 | (empty `sessions/`) | | | scanned 0, exit 0 | Boundary (zero) | Sc 15 |

Expected counts for rows 1-16: scanned 14 (rows 1-14), connector skipped 2, would change / changed 6 (main 2, scheduled 1, verifier 1, system 1, heartbeat 1), unparseable 1, exit 1. Without row 14: exit 0.

### DS-3: Creators (FR-009, FR-010; T-05, T-06, T-09)

| # | Creator | Want stored place | Category | Traces to |
|---|---|---|---|---|
| 1 | `GetOrCreateMainSession(ws, agent)` | "" | Happy | Sc 11 |
| 2 | `NewScheduledSession(agent)` | "" | Happy | Sc 11 |
| 3 | `GetOrCreateScheduledSession(id, agent)` (new id) | "" | Happy | Sc 11 |
| 4 | `GetOrCreateScheduledSession(id, agent)` (same id again) | same as row 3 | Edge | Sc 11 |
| 5 | `NewVerifierSession(agent)` | "" | Happy | Sc 11 |
| 6 | task executor task session | "" | Happy | Sc 11 |
| 7 | plan engine minted session | "" | Happy | Sc 11 |
| 8 | `NewChannelSession("telegram", "telegram.1", "peer-9", ...)` | telegram | Regression | Sc 12 |
| 9 | REST `POST /api/v1/sessions` (type chat) | webchat | Regression | Sc 12 (T-23) |
| 10 | delegate launched by `SteerLauncher.Launch` | "" | Regression | Sc 12 |

### DS-4: Redirect routes (FR-002, FR-006, FR-007, FR-008, FR-028; T-02..T-04, T-08, T-15, T-16, T-18, T-19)

| # | Session | Stated origin | Turn | Want | Category | Traces to |
|---|---|---|---|---|---|---|
| 1 | Main, stored `main` | web | running | webchat / session id | Bug repro | Sc 1 |
| 2 | Main, fresh | web | running | webchat / session id | Happy | Sc 2 |
| 3 | Main, fresh | web | idle | webchat / session id | Edge | Sc 4 |
| 4 | Main of the pair bound to `telegram.a`; running turn from a captured request of `tg-chat-42` | control `telegram.a` / `tg-chat-42`, sender `telegram:42` | running | new capture (`telegram.a`, `tg-chat-42`, bound pair, `telegram:42`); new turn calls `answer_request` with it → exactly one outbound to `telegram.a` / `tg-chat-42`, no refusal frame | Alternate (changed) | Sc 5 |
| 4d | as row 4; the new turn answers in plain text only | control `telegram.a` / `tg-chat-42` | running | no connector outbound, exactly one refusal | Error (regression) | Sc 5d |
| 4b | as row 4, plus an earlier captured request from `tg-chat-77` | control `telegram.a` / `tg-chat-42` | running | outbound only to `tg-chat-42`; unknown request id refused, nothing sent | Error (security) | Sc 5b |
| 4c | as row 4; before admission the config unbinds `telegram.a`, or rebinds it to pair B (Main N absent / present) | continuation for `telegram.a` / `tg-chat-42` | idle after stop | nothing written in M or N, N not created, no provider call, curated notice with raw instruction to `telegram.a` / `tg-chat-42` | Error (security) | Sc 5c |
| 4e | bound Main M idle; intake closed by shutdown; variant: M's ledger has one unreadable line | continuation for `telegram.a` / `tg-chat-42` | idle | capture discarded, id refused, transcript keeps instruction, notice with raw instruction; variant: error log + "could not be withdrawn" notice | Error | Sc 5e |
| 5 | Sc 3 rows | web | running | webchat / session id | Happy | Sc 3 |
| 6 | channel telegram, peer r2Chat | web | running | telegram / r2Chat, same session, transcript has both | Regression (Q2) | Sc 6 |
| 7 | channel telegram, no peer | web | running | webchat / session id | Error (D2b) | Sc 7 |
| 8 | unknown id | web | n/a | refused "not found" | Error | Sc 8 |
| 9 | Main | web, instruction "   " | running | refused usage; turn keeps running | Error | Sc 8 |

### DS-5: Script refusals and failures (FR-020, FR-022, FR-024, FR-030; T-33, T-34)

| # | Setup | Args | Exit | Category | Traces to |
|---|---|---|---|---|---|
| 1 | `--home` path does not exist | `--home /nonexistent` | 2 | Error | Sc 19 |
| 2 | home exists, no `sessions/` | `--home <h>` | 2 | Error | Sc 19 |
| 3 | DS-2 home | `--apply` (no `--backup-dir`) | 2 | Error | Sc 19 |
| 4 | DS-2 home; backup dir has one file | `--apply --backup-dir <d>` | 2 | Error | Sc 19 |
| 5 | DS-2 home; backup dir path is not writable (chmod 0500 parent) | `--apply --backup-dir <d>` | 2 | Error (permission) | Sc 19 |
| 6 | DS-2 home | no args | 2 | Error | Sc 19 |
| 7 | DS-2 home; backup dir exists and is empty | `--apply --backup-dir <d>` | 1 (row 14 present) | Boundary (empty dir accepted) | Sc 16 |
| 8 | DS-2 home without row 14; the rename whose destination is the 2nd selected session's live `<home>/sessions/<name>/meta.json` raises `OSError` (injected via `importlib` + `unittest.mock`; every other rename runs for real) | `--apply --backup-dir <d>` | 3 | Error (apply part-way) | Sc 23 |
| 9 | home after row 8, fault removed | `--apply --backup-dir <new empty d2>` | 0 | Recovery (re-run) | Sc 23 |

## TDD Plan

Build tags: the neighbouring tests (`stop_redirect_root_*_test.go`, `parent_wake_*_test.go`, `sessioncore_u1_*_test.go`, `websocket_redirect_streaming_test.go`) have no `//go:build` line; new Go test files follow them. Run with `-tags goolm,stdjson` as CI does (`.github/workflows/cross-platform.yml`, job `matrix`, step "Test": `go test -tags goolm,stdjson -short -timeout 30m ./...`; line 135 on `ef6845a5d`; also `/workspace/omnipus/CLAUDE.md`). The `s1seams` tag does not exist on `origin/release/v0.1.1` (`git grep s1seams` finds nothing); do not use it. Targeted runs only, e.g. `heavy go test -p 1 -tags goolm,stdjson -run 'TestRedirect1931_' ./pkg/agent/`.

Reuse fixtures: `newSteerAL`, `r2ChannelChat`, `r1AwaitProvider`, `r2AwaitOutbound`, `r2HoldStopFence`, `pwChannelSession`, `boundConnFixture` / `connMsg` (`pkg/agent/connector_intake_test.go:21-39`), `newAddrFixture` with its router `f.r`, `addrDrainOutbound`, `addrDrainInbound` (`pkg/agent/address_router_test.go:57-92`) (pkg/agent); `NewUnifiedStore(t.TempDir())` (pkg/session); `createSessionWithKey` / `newSendOriginSrv` (`pkg/gateway/rest_sessions_new_chat_key_test.go:43,85`). For T-08/T-19 the connector message's `Channel` MUST equal its instance key (`telegram.a`), as `BaseChannel.SetInstanceID` makes it in production (`pkg/channels/base.go:183-192`), so the control's channel name matches the running turn (`resolveSessionIDByChannelChat`, `turn.go:1431`). There is no fallback for T-08/T-19: if the fixtures cannot hold a bound Main turn running, or cannot script the new turn to call the real `answer_request` tool, the test author reports blocked; a non-Main or `RedirectSessionTurn`-based substitute, or a direct `f.r.Reply` call standing in for the turn's successful answer, is not acceptable (`f.r.Reply` stays only for the unknown-id and discarded-id refusals). T-08's `binding_change_*` subtests and T-25 drive the continuation directly, as `stop_redirect_root_r2_test.go::TestStopRedirectRoot_G2_NoticeQuotesInstruction_AdmissionRefusedAfterShutdown` does: build the message with `ordinaryRedirectMessage(M, instr, "telegram:42", "telegram.a", "tg-chat-42")`, change the config with `SwapConfig` or close intake with `stopActiveRequestIntake`, then call `continueOrdinaryAfterStop`. Tests call only symbols that exist on the release (no direct call of `admitBoundConnectorInputForMain`), so the `pkg/agent` test build still compiles at RED.

| ID | Order | Test (file :: name) | Level | Traces to | Description |
|---|---|---|---|---|---|
| T-30 | 1 | `scripts/oneoff-1931-clear-session-kind-labels.test.py` :: `test_dry_run_writes_nothing` | Unit (script) | Sc 15 | DS-2; hash every file before/after; counts; no backup |
| T-31 | 2 | same :: `test_apply_backs_up_then_clears_only_legacy_labels` | Unit (script) | Sc 16 | DS-2 with DS-5 row 7 (backup folder created empty beforehand, accepted); parsed equality except `channel`, key order, file mode, backup bytes, manifest, counts, exit 1 with the six changes kept |
| T-32 | 3 | same :: `test_second_run_is_noop` and `test_restore_from_backup_is_byte_identical` | Unit (script) | Sc 17, 18 | Second run on both Scenario 17 homes: 0 selected, no file or backup written, exit 1 (row 14 listed) and exit 0; restore before any build run is byte-identical |
| T-33 | 4 | same :: `test_refusals_write_nothing` | Unit (script) | Sc 19 | DS-5 rows 1-6 as subtests (exit 2, every session identity file byte-identical); row 7 is in T-31 |
| T-34 | 4b | same :: `test_apply_failure_part_way_then_rerun` | Unit (script) | Sc 23 | DS-5 rows 8, 9: load with `importlib.util.spec_from_file_location`; keep the real `os.replace` and patch the module's `os.replace` with a side effect that raises `OSError` only when the destination is the 2nd selected session's live `<home>/sessions/<name>/meta.json` (by FR-022 order) and otherwise calls the real one; inside that side effect, before raising, record that the backup folder holds every selected original and `manifest.txt`; call `main([...])`; assert that record, exit 3, output, file states, no temp file, backup contents; then re-run unpatched with a new backup folder (exit 0) and restore from the first complete backup (byte-identical) |
| T-01 | 5 | `pkg/session/session_kind_channel_1931_test.go` :: `TestOutsideReplyAddress_Table` | Unit | Sc 9 | DS-1 |
| T-05 | 6 | same :: `TestNewSessions_KindOnlyCreatorsStoreEmptyChannel` | Unit | Sc 11 | DS-3 rows 1-5; `GetMeta` and raw `meta.json` |
| T-06 | 7 | same :: `TestNewSessions_UnchangedCreatorsKeepChannel` | Unit | Sc 12 | DS-3 row 8 |
| T-09 | 8 | `pkg/agent/session_kind_creators_1931_test.go` :: `TestTaskSessions_EngineCreatedSessionsStoreEmptyChannel` | Integration | Sc 11, 12 | DS-3 rows 6, 7, 10 |
| T-10 | 9 | `pkg/agent/session_kind_wake_1931_test.go` :: `TestWakeAddress1931_FollowsOutsideReplyRule` | Integration | Sc 10 | DS-1 rows 1, 2, 11-16 via `ensureOrdinaryRootRecord` + `reconstructSteeredTurn` (pattern of `parent_wake_channel_red_test.go`); after each wake, re-read the stored `channel` and compare it with the seed (that field only; same harness, no new one) |
| T-11 | 10 | same :: `TestReportingTarget1931_FollowsOutsideReplyRule` | Integration | Sc 10 | Delegation from DS-1 rows 1, 2, 12, 13, 14, 16; check child's `SteeredBy.ReportingTarget` |
| T-12 | 11 | same :: `TestChannelIndex1931_RebuildIndexesOnlyOutsideAddresses` | Integration | Sc 10 | Subtest `membership`: seed DS-1 rows 1, 2, 12, 13, 14, 16, 19; only rows 12 (`telegram.1/peer-9`) and 19 (`telegram/peer-9`) indexed. Subtests `collision_*`: each DS-1b set in its own store; the key maps to the DS-1b winner |
| T-02 | 12 | `pkg/agent/session_kind_redirect_1931_test.go` :: `TestRedirect1931_MainChatFromWebContinuesInWebChat` | Integration | Sc 1, 2 | Subtests `legacy_main_label` (Scenario 1 exact) and `fresh_main`; `RedirectSessionTurn(ctx, id, instr, owner, "web")`; nil error, turn stopped, transcript user message, one provider call, outbound `webchat`/id |
| T-03 | 13 | same :: `TestRedirect1931_KindOnlyChatsFromWebContinueInWebChat` | Integration | Sc 3 | Scenario 3 table as subtests |
| T-04 | 14 | same :: `TestRedirect1931_IdleMainChatRunsOneTurnInWebChat` | Integration | Sc 4 | DS-4 row 3 |
| T-08 | 15 | same :: `TestRedirect1931_MainChatTypedInConnectorIsAnsweredThroughItsCapturedRequest` | Integration | Sc 5, 5c, 5d | DS-4 rows 4, 4c, 4d. `boundConnFixture`; admit `connMsg` (Channel = `telegram.a`, chat `tg-chat-42`) with `admitBoundConnectorInput`; hold its Main turn running with the stop-redirect provider fixtures; call `RequestRedirectByChannelChat(ctx, "telegram.a", "tg-chat-42", "telegram:42", instr)`. Subtest `answered` (row 4): nil error; turn stopped; one new capture in `RequestLedger()` with the DS-4 row 4 source and sender; exactly one transcript entry holding the instruction, under that id; the new turn's provider input contains `request_id="<id>"`; the provider scripts the new turn to call the real `answer_request` tool with that id, then return closing text → exactly one outbound in total (`addrDrainOutbound`), to `telegram.a`/`tg-chat-42` with `Return.RequestID == id`, and no "Not sent to" error frame. Subtest `plain_only` (row 4d): the new turn returns plain text only → no connector outbound, exactly one error frame. Subtests `binding_change_unbound`, `binding_change_rebound_new_main`, `binding_change_rebound_existing_main` (row 4c, Scenario 5c; continuation driven directly, see note above): nothing written in M or N, N absent when it was absent, no provider call, one notice to `telegram.a`/`tg-chat-42` that contains the raw instruction and contains neither `connector intake`, `request_id=` nor any pair A/B workspace or agent id |
| T-19 | 15b | same :: `TestRedirect1931_MainConnectorRedirectCannotAnswerAnotherChat` | Integration | Sc 5b | DS-4 row 4b: second captured request from `tg-chat-77` (sender `telegram:77`) admitted first; after the redirect, the new turn answers the redirect's id with the real `answer_request` tool → the only outbound goes to `tg-chat-42`; `f.r.Reply` with an id not in M's ledger returns the refusal and publishes nothing |
| T-25 | 15c | same :: `TestRedirect1931_MainConnectorRedirectNotStartedIsWithdrawn` | Integration | Sc 5e | DS-4 row 4e, continuation driven directly with intake closed. Subtest `withdrawn`: one capture for the instruction in M's ledger with `Discarded == true`; `f.r.Reply` with its id refused, nothing published but the notice; transcript entry under the id holds the raw instruction; no provider call; notice to `telegram.a`/`tg-chat-42` quotes the raw instruction and has no `request_id=`; then `admitBoundConnectorInput` of the same text from `telegram.a`/`tg-chat-42` gives a new id that `Ledger.Resolve` accepts while the first stays `ErrDiscarded`. Subtest `withdraw_fails`: seed M's ledger file with one newline-terminated non-JSON line first (`Ledger.Put` still appends; `Ledger.Discard` cannot read the file); the notice says the instruction could not be withdrawn, and the log has an error naming M and the request id |
| T-15 | 16 | same :: `TestRedirect1931_ConnectorChatWithoutPeerFromWebContinuesInWebChat` | Integration | Sc 7 | DS-4 row 7 |
| T-16 | 17 | same :: `TestRedirect1931_ErrorsUnchanged` | Integration | Sc 8 | DS-4 rows 8, 9 |
| T-18 | 18 | same :: `TestRedirect1931_ConnectorChatFromWebViewKeepsTodaysRoute` | Integration | Sc 6 | DS-4 row 6: same session id, reply `telegram`/`r2Chat` with that `SessionID`, transcript has instruction (user) then reply (assistant) |
| T-20 | 19 | `pkg/gateway/websocket_redirect_main_1931_test.go` :: `TestWSRedirect1931_MainChatRedirectFrameIsAcceptedAndStreamsInChat` | E2E (server) | Sc 1 | WS harness as in `TestWSRedirect_StreamingTextTurnOverTheRealHTTPProvider`; legacy Main; redirect frame; no `error` frame; continued turn streams to that session |
| T-21 | 20 | `pkg/gateway/rest_sessions_channel_1931_test.go` :: `TestSessionList1931_ChannelIsStoredPlace` | Integration | Sc 13 | Scenario 13 rows as subtests |
| T-23 | 20b | same :: `TestSessionCreate1931_RESTChatStoresWebchat` | Integration | Sc 12 | DS-3 row 9: `POST /api/v1/sessions` through the real handler (`createSessionWithKey` pattern, body without `type`, and with `"type":"task"`); 201; cold-open `GetMeta` and raw `meta.json` have `channel: "webchat"`; `GET /api/v1/sessions` shows `webchat` |
| T-22 | 21 | `pkg/gateway/session_channel_contract_1931_test.go` :: `TestSessionContract1931_ChannelDescribedAsPlace` | Unit | Sc 14 | Read `../../contracts/components/schemas/Session.yaml` and `inboundschemas/Session.yaml`; byte-equal; YAML: `channel` in `required`, `type: string`, no `enum`; description contains `webchat` and `type` |
| DOC-1 | 22 | Shell: `grep -n "Correction" docs/internal/specs/parent-wake-visibility-spec.md` | Doc | FR-018 | Dated correction with the FR-018 meaning next to PW-D1 and the scenario |
| DOC-2 | 22b | Docs review of `docs/using-omnipus-ui.md` "Stop and redirect commands" | Doc | FR-031 | Docs verifier (not the developer) checks the new paragraph states the four destinations of FR-031 and matches FR-002, FR-007, FR-028; diff limited to that section |
| SC-3..8 | 23 | Shell checks SC-003..SC-006, SC-008 | Review | FR-009, FR-011, FR-015, FR-028 | Run at checkpoint; output saved |
| HO-1..HO-10 | 24 | Hands-on (below) | Manual | Sc 1-7, 13, 20-22 | Two testers |

The script tests are not wired into CI (one-off tool); the test author runs them and saves the log and exit code as checkpoint evidence.

### RED list (must fail on `origin/release/v0.1.1` before the fix)

| Test | Why it fails today |
|---|---|
| T-30..T-34 | Script does not exist |
| T-08 `TestRedirect1931_MainChatTypedInConnectorIsAnsweredThroughItsCapturedRequest` | No capture is written for the instruction (the continuation carries `SessionID`, so admission is skipped, `connector_intake.go::boundConnectorPair`); the model input has no request id for it. `plain_only` may pass today (control). `binding_change_*`: today a turn runs anyway (provider call) |
| T-19 `TestRedirect1931_MainConnectorRedirectCannotAnswerAnotherChat` | No capture for the redirect exists to answer |
| T-25 `TestRedirect1931_MainConnectorRedirectNotStartedIsWithdrawn` | No capture is made today, so there is nothing discarded to find; the notice exists today (that part passes) |
| T-01 `TestOutsideReplyAddress_Table` | Method does not exist (compile failure of the `pkg/session` test build; expected at RED) |
| T-02 `TestRedirect1931_MainChatFromWebContinuesInWebChat` | "is a main chat with no recorded chat id" (both subtests: a fresh Main stores `main` today) |
| T-03 `TestRedirect1931_KindOnlyChatsFromWebContinueInWebChat` | scheduled/task/verifier subtests with a label are refused; place-`""` and `chat/webchat` subtests pass (controls) |
| T-04 `TestRedirect1931_IdleMainChatRunsOneTurnInWebChat` | Refused the same way |
| T-05 `TestNewSessions_KindOnlyCreatorsStoreEmptyChannel` | Creators write labels |
| T-09 `TestTaskSessions_EngineCreatedSessionsStoreEmptyChannel` | Rows 6, 7 write `system` (row 10 passes) |
| T-10 `TestWakeAddress1931_FollowsOutsideReplyRule` | Rows 14 and 16 stream to the peer today |
| T-11 `TestReportingTarget1931_FollowsOutsideReplyRule` | Rows 14 and 16 report to the peer today |
| T-12 `TestChannelIndex1931_RebuildIndexesOnlyOutsideAddresses` | Row 16 is indexed today |
| T-15 `TestRedirect1931_ConnectorChatWithoutPeerFromWebContinuesInWebChat` | Refused today |
| T-20 `TestWSRedirect1931_MainChatRedirectFrameIsAcceptedAndStreamsInChat` | Error frame today |
| T-21 `TestSessionList1931_ChannelIsStoredPlace` | New Main shows `main` |
| T-22 `TestSessionContract1931_ChannelDescribedAsPlace` | Old description ("Channel identifier that initiated this session") |
| DOC-1 | No correction yet |
| DOC-2 | Section has no destination paragraph yet |

Green before and after (regression guards): T-06, T-16, T-18, T-23. (T-08 is no longer a regression guard: grill round 1, B1.)

### Existing tests that change (test author edits, citing ADR-20261128 and this spec)

| Test | Change |
|---|---|
| `pkg/agent/stop_redirect_root_r3_test.go` :: `TestStopRedirectRoot_R1_ChannelChatWithoutChatIDIsRefusedAndTurnKeepsRunning` | Delete; superseded by T-15 (ADR D2(b)). The one sanctioned test removal. |
| `pkg/agent/parent_wake_channel_red_test.go` :: `TestWakeChannel_MainBecomesWebchatStoredUntouched` | Row "webchat peer" (line 68): want `webchat` + session id (ADR D2 corner). Other rows unchanged. |

No other existing test changes: with no read-time clean-up, `parent_wake_session_red_test.go:70` (`NewSession(chat, "main")` reads back `"main"`) and the `sessioncore_u1_*` fixtures keep passing as they are.

### Must pass unchanged (regression)

`pkg/agent`: `TestStopRedirectRoot_StopsCurrentTurnThenRunsExactlyOneTurnWithInstruction`, `TestStopRedirectRoot_IdleChatRunsExactlyOneTurnWithInstruction`, `TestStopRedirectRoot_G1_WebFrameRedirectOfChannelChatContinuesOnRealChannelAndChat`, `TestStopRedirectRoot_G1_UndeliveredNoticeGoesToTheChatsRealChannelAndChat`, all `TestStopRedirectRoot_G2_*`, `TestStopRedirectRoot_G3_ChannelControlBoundaryStopsRootChatAndContinuesOnce`, `TestStopRedirectRoot_R2_NoticeQuotesInstruction_RouteError`, `TestStopRedirectRoot_R3_ChannelChatMessageNotSavedRefusesTurnWithFixedReply`, all `TestWakeSession_*` (FR-013), and every test in `connector_intake_test.go` and `address_router_test.go`, in particular `TestMainConnectorDefaultSend_RefusedOnceWithNote` and `TestMainConnectorDefaultSend_UnboundAndNonMainStillSend` (FR-028 (c)). `pkg/gateway`: `TestWSRedirect_StreamingTextTurnOverTheRealHTTPProvider`, `TestSessionList_*`. `pkg/session`: all `TestSessionCoreU1_*`.

Regression dataset: DS-4 rows 6, 8, 9 and DS-3 rows 8-10.

## Hands-on Scenarios (ADR section 9, plus the script)

Run on a Fly test instance seeded with a copy of legacy-style sessions (Main `channel: "main"`, a scheduled chat `scheduled`, a task chat `system`, a Telegram conversation), by two testers on different models from the developer and from each other. Each check saves a screenshot or log.

| ID | Check | Pass when | Traces to |
|---|---|---|---|
| HO-1 | Main chat in the web: start a long turn, type `/stop-redirect <text>` | Turn stops; instruction answered in the same web chat; no "Not stopped" | FR-002, FR-008 |
| HO-2 | Main via a bound Telegram: start a long turn from Telegram, then type `/stop-redirect <text>` in the same Telegram chat; a second Telegram account has sent the Main a message earlier | The answer to the instruction arrives in the first Telegram chat only; nothing arrives in the second account's chat; the Main's web view shows the instruction and no "Not sent to telegram" refusal for that answer | FR-006, FR-028 |
| HO-3 | HO-1 in a scheduled chat and a task chat | Same as HO-1 | FR-002, FR-008 |
| HO-4 | A Telegram conversation redirected from its web view | Same session continues; reply arrives in Telegram; web view shows instruction and reply | FR-007 |
| HO-5 | Plain message mid-turn in Main | Queues; nothing cancelled | FR-016 |
| HO-6 | Main via Telegram delegates to a helper | Record where the result appears and whether the Telegram requester gets an answer (observation only; settles ADR G2) | Non-behaviour |
| HO-7 | `GET /api/v1/sessions` after HO-10 | No non-connector session shows a legacy label; Main `""`; connector sessions keep their channel | FR-014, FR-026 |
| HO-8 | Woken parent (PW-D1) | Streams live in the open Main web chat | FR-003 |
| HO-9 | After HO-1..HO-8 on the new build: rename a cleaned legacy Main, change its model or effort, send it a message, and create a new scheduled chat; then binary rollback per FR-029 (stop, previous build, start), **no backup restored** | `/stop-redirect` from the web on the cleaned legacy Main and on the new-build chat both answer in their web chats; HO-8 still streams in the web chat; no crash; the rename, model/effort change and message are still there | FR-017, FR-029 |
| HO-10 | Run FR-026 steps on the seeded instance: stop, dry run, `--apply` with backup, dry run again, new build, start | Counts match the seeded labels; backup folder holds `manifest.txt`; second dry run reports 0 "would change" and exits 0; instance starts. Any exit 1: stop, no build started, chief told (FR-026) | FR-020..FR-026 |

**Live Mac instance (Mac squad, after the founder's landing yes):** same steps as HO-10, then HO-1 and HO-7 on the live instance. First check `python3 --version` on the Mac (3.8 or newer); if missing, stop and tell the chief. Home path: the live instance's Omnipus home (❓ exact path known to the Mac squad). Backup folder: outside `<home>/sessions`, e.g. `<home>-backup-1931-<UTC date>`. Keep the backup until the founder says it can go; it is for offline recovery before the instance runs again (FR-027), never for a rollback after the new build has run (FR-029). On exit 3 follow FR-030; on exit 1 (unparseable identity file), exit 2 or an unexpected second dry run, stop and tell the chief before starting anything (FR-026). Fly test gateway: run the same steps if `<home>/sessions` holds any session; otherwise skip and record "no data".

## Traceability Matrix

| Requirement | User story | Scenarios | Tests |
|---|---|---|---|
| FR-001 | US-2 | Sc 9 | T-01 |
| FR-002 | US-1 | Sc 1, 2, 3, 4, 7 | T-02, T-03, T-04, T-15, T-20, HO-1, HO-3 |
| FR-003 | US-2 | Sc 10 | T-10, HO-8 |
| FR-004 | US-2 | Sc 10 | T-11 |
| FR-005 | US-2 | Sc 10 (DS-1, DS-1b) | T-12 |
| FR-006 | US-1 | Sc 5 | T-08, HO-2 |
| FR-028 | US-1 | Sc 5, 5b, 5c, 5d, 5e | T-08, T-19, T-25, existing `TestMainConnectorDefaultSend_*` and `connector_intake_test.go` (unchanged), SC-008, HO-2 |
| FR-007 | US-1 | Sc 6 | T-18, existing G1 tests, HO-4 |
| FR-008 | US-1 | Sc 1, 2, 3, 4, 8 | T-02, T-03, T-04, T-16, T-20, HO-1 |
| FR-009 | US-3 | Sc 11 | T-05, T-09, SC-004 |
| FR-010 | US-3 | Sc 12 | T-06, T-09, T-23 |
| FR-011 | US-3 | Sc 13 | T-21 (legacy row), SC-005 |
| FR-013 | US-5 | Sc 10 | T-10 (stored `channel` re-read), existing `TestWakeSession_*`, `TestWakeChannel_*` |
| FR-014 | US-3 | Sc 13 | T-21, HO-7 |
| FR-015 | US-3 | Sc 14 | T-22, SC-003, SC-006 |
| FR-016 | US-5 | Sc 21 | HO-5 |
| FR-017 | US-5 | Sc 22 | HO-9 |
| FR-018 | US-4 | (document) | DOC-1 |
| FR-019 | US-4 | Sc 15-19, 23 | T-30..T-34 (file exists, stdlib only: `python3 -I` run passes) |
| FR-020 | US-4 | Sc 15, 19 | T-30, T-33 |
| FR-021 | US-4 | Sc 16 | T-31 |
| FR-022 | US-4 | Sc 16, 17, 19, 23 | T-31 (incl. DS-5 row 7), T-32, T-33, T-34 |
| FR-023 | US-4 | Sc 16, 23 | T-31, T-34 |
| FR-024 | US-4 | Sc 15, 16, 17, 19, 23 | T-30, T-31, T-32, T-33, T-34 |
| FR-025 | US-4 | Sc 17 | T-32 |
| FR-026 | US-4 | Sc 20 | HO-10, HO-7 |
| FR-027 | US-4 | Sc 18, 23 | T-32, T-34 |
| FR-029 | US-5 | Sc 22 | HO-9 |
| FR-030 | US-4 | Sc 23 | T-34 |
| FR-031 | US-1 | (document) | DOC-2 |

(FR-012 was removed with the read-time clean-up; numbering kept so earlier references stay valid. FR-028..FR-031 were added in the grill round 1 fix.)

## Out of Scope

- Plain message queueing: unchanged; only `/stop-redirect` is in scope.
- Frontend logic: no change; optional comment fix at `src/lib/api/sessions.ts:64-65`.
- Changing the Main default-send refusal, the channel-control interface (`pkg/channels`), or adding platform message ids / display names to a redirect's capture (accepted limits, FR-028 (d)).
- Restoring backups after the new build has run; any automatic rollback logic.
- User-guide changes beyond the one paragraph of FR-031.
- Read-time clean-up, rewrite-on-save, boot migration or any upgrade logic in the product.
- Redirect routing for a connector conversation's web view (founder Q2: unchanged).
- Where delegation results go for a connector-started Main turn (ADR G2): HO-6 observes only.
- The bus message channel `"system"`.
- Contract shape changes (fields, enums).
- Ownership of `pkg/agent/`, `pkg/gateway/` (ADR G5): PM assigns.

## Ambiguity Warnings

| # | Ambiguity | Resolution |
|---|---|---|
| A1 | Whitespace in place/peer | Trimmed; whitespace-only = empty (DS-1 rows 17, 18), matching today's `ordinaryRedirectRoute` |
| A2 | Script label match | Exact, case-sensitive, five labels, non-`channel` types only (DS-2 rows 9-13) |
| A3 | Deleting the R1 test | Sanctioned by ADR D2(b); T-15 replaces it |
| A4 | Script rewrite formatting differs from Go's (Go escapes `<`, `>`, `&` as `<` etc.) | Accepted: only parsed values and key order must match (FR-021, FR-023); the product reads both |
| A5 | T-08 fixture for "Main reached via bound connector" | Resolved: `boundConnFixture` + `RequestRedirectByChannelChat`, no fallback (TDD plan note) |
| A9 | Who may redirect a bound Main from a connector | Unchanged: `command_channel_controls.go::commandSessionByChannelChat` first looks up the instance-keyed per-chat index (an older per-chat connector session of that chat, if any, is found there and keeps today's non-Main continuation, FR-028 (e)); otherwise only a chat whose (instance, chat) matches a running turn finds it (`turn.go::resolveSessionIDByChannelChat`). Another chat with neither gets today's "no conversation here to redirect yet". FR-028 adds no new way in |
| A10 | Security review of FR-028 | One new call of an existing, reviewed admission path with adapter-supplied route and sender (same trust as A1-2 in `connector_intake.go`'s header; control runs after the sender allow check, `channels/controlparse.go::BaseChannel.interceptSessionControl`), plus the expected-Main check and the withdrawal. Proportionate security review of that call site and of the `connector_intake.go` diff at checkpoint, per the normal lane rule |
| A6 | Python 3 on the live Mac and the exact home path | ❓ Mac squad checks before HO-10 on live; stop and report if Python is missing |
| A7 | Per-agent legacy session folders (`<agent home>/sessions`) | Not scanned: they are converted into the shared store at boot (`loop_construct.go:40-75`); the script scans `<home>/sessions` only |
| A8 | Founder note's premise "old `main` not recognised by the rule" | Does not hold under D2 (rule keys on `type`); order rule kept for API truth and to stop a running build overwriting the change. Shown by T-02 `legacy_main_label`, T-20 (legacy Main) and T-21 (stored `"main"` in the API); no hands-on start before the script (former HO-11 dropped: it contradicted FR-026) |

## Evidence

| Claim | Evidence | Label |
|---|---|---|
| Web frame passes `"web"` as stated channel | `git show origin/release/v0.1.1:pkg/gateway/websocket_redirect.go` line 46 | ✅ |
| `"web"`/`""` count as not stated; refusal without peer id | `stop_redirect_root.go:110-128` | ✅ |
| Connector command states channel and chat id | `command_channel_controls.go:60-73`; adapter passes `c.Name()`, chat, sender after its allow check (`pkg/channels/controlparse.go:48-79`) | ✅ |
| Main connector default send is refused; redirect continuation carries `SessionID` so admission skips it; capture and return route made only by admission | `loop_publish.go:51-55`; `connector_egress.go:16-31,50-64`; `stop_redirect_root.go:95-101,157-181`; `connector_intake.go:49-51,160-211`; `revive_support.go:247-248` | ✅ |
| A connector capture needs only request id, receiver, source session, instance and chat to be answerable; the answer goes to (instance, chat) with a `Return` route | `pkg/addressing/addressing.go:150-185`; `address_router.go` `Reply` / `replyViaConnector` | ✅ |
| FR-028 delivers end to end once built | not built or run | 🔶 (medium-high); proven by T-08, T-19, T-25, HO-2 |
| `boundConnectorPair` reads one config snapshot per call; admission calls it itself, then writes; `SwapConfig` can replace config between calls (why the Main pin must sit inside admission) | `connector_intake.go::boundConnectorPair`, `::admitBoundConnectorInputCtx`; `loop_config.go::SwapConfig` | ✅ |
| `session.MainSessionID(workspace, agent)` computes a pair's Main id | `pkg/session/main_session.go::MainSessionID` | ✅ |
| A captured request stays answerable until discarded (`Resolve` checks `Discarded`, not `Delivered`); `settleRequest` + `Ledger.Discard` is admission's own withdrawal | `pkg/addressing/ledger.go::Ledger.Resolve`, `::Ledger.Discard`; `request_reminder.go::settleRequest`; `connector_intake.go::admitBoundConnectorInputCtx` step 5 | ✅ |
| `runRevivedOrdinaryTurn` returns false when shutdown closed intake; existing test forces it with `stopActiveRequestIntake` | `revive_support.go::runRevivedOrdinaryTurn`; `active_requests.go::stopActiveRequestIntake`; `stop_redirect_root_r2_test.go::TestStopRedirectRoot_G2_NoticeQuotesInstruction_AdmissionRefusedAfterShutdown` | ✅ |
| `Ledger.Put` appends after checking only the tail; `Discard` parses the whole file and fails on a terminated corrupt line (T-25 `withdraw_fails` seam) | `ledger.go::Ledger.Put`, `::repairTornTail`, `::readAllLatest` | ✅ code reading; 🔶 until T-25 runs |
| The not-applied notice quotes its `cause` and `msg.Content` verbatim; normal intake logs the admission error and sends only the refusal | `stop_redirect_root.go::reportUndeliveredOrdinaryRedirect`; `loop.go::admitAndDispatch` | ✅ |
| A successful `answer_request` suppresses the round's default send before the Main refusal | `loop_wire.go::registerCoreTools`; `tools/answer_request.go::AnswerRequestTool.Execute`; `tools/message_address.go::MessageTool.executeReplyFormWithMedia`; `loop_publish.go::publishResponseIfNeeded` | ✅ |
| A present unparseable identity file stops boot on any build | `unified_conv.go::convNormalizeSavedChatDirs`; `loop_construct.go::initializeConvertedSessions`; `loop.go::NewAgentLoop`; `conv_boot_step_test.go::TestConvBootStep_RefusesOnCorruptSavedChat` (read, not run) | ✅ |
| Bound fixture and router fixture exist | `connector_intake_test.go:21-39,151-178`; `address_router_test.go:57-92` | ✅ |
| Production keeps a message's channel equal to its instance key | `pkg/channels/base.go:177-192` | ✅ |
| Index winner ordering | `loop_session.go:55-69` | ✅ |
| User guide section | `docs/using-omnipus-ui.md:341-357` | ✅ |
| No existing test asserts the stored channel of a REST-created chat | `git grep` of `pkg/gateway/*_test.go` for `webchat` / POST `/api/v1/sessions`; `rest_sessions_new_chat_key_test.go:81` checks only `new_chat_key` | ✅ (search) |
| Four routing sites and their rules | `wake_address.go:8-12`, `steer_launcher.go:657-661`, `loop_session.go:62` | ✅ |
| Label writers | `unified.go:738,812,849,886`; `task_executor.go:845`; `plan_engine.go:2285` | ✅ |
| Identity file format and write | `unified_meta_files.go:57-90,354-363` (`MarshalIndent` two spaces, `0o600`, atomic) | ✅ |
| Shared store path `<home>/sessions/<id>/meta.json`; per-agent dirs converted at boot | `shared_session_store.go:65`, `loop_construct.go:40-75`, `unified_list.go` | ✅ |
| Python test convention | `scripts/ci-quarantine.test.py` (unittest); `.github/workflows/pr.yml`, job `ci-required`, step "Prove the quarantine guard and aggregate reject unlisted failures" runs `python3 -I scripts/<name>.test.py` (lines 4030-4032 on `ef6845a5d`; ⚠️ round 1 cited line 3938, which is an unrelated `if: always()`) | ✅ |
| Issue text | `gh issue view 1931` | ✅ |
| R1 refusal test conflicts with D2(b); G1 tests pin Q2 behaviour | `stop_redirect_root_r3_test.go:24`; `stop_redirect_root_r2_test.go:100,114` | ✅ |
| PW tests: only the "webchat peer" row changes | `parent_wake_channel_red_test.go:68` (the row; table at 56-69); `parent_wake_session_red_test.go:58-71` | ✅ |
| Neighbouring tests have no build line; CI tags `goolm,stdjson`; `s1seams` absent | `git grep "^//go:build"`; `.github/workflows/cross-platform.yml`, job `matrix`, step "Test" (line 135 on `ef6845a5d`; ⚠️ round 1 cited line 120, a comment); `git grep s1seams` on `ef6845a5d` (no match, confirmed independently by grill round 2) | ✅ |
| PW-D1 wording and founder source | `parent-wake-visibility-spec.md:14,18,574-581` | ✅ |
| Routing correct for legacy labels before the script | Follows from FR-001 keyed on `type`; not run | 🔶 (high); proven by T-02, T-20 |
| Previous build routes `""` to web at the four sites | `stop_redirect_root.go:119-121`, `wake_address.go:9`, `steer_launcher.go:658`, `loop_session.go:62` | ✅ (code reading); 🔶 behaviour until HO-9 |
| Identity file holds more than `channel` (why whole-file restore after use loses data) | `unified_meta_files.go:66-94` | ✅ |
| Python on the live Mac; live home path | not checked | ❓ (A6) |
| Self-check | Round 2 fix: re-opened every symbol added or changed in this round on `ef6845a5d` (`git show`), and re-ran the cited-file change inventory; no build or test run. Round 1 rows left unchanged were checked by the grill round 2 audit, except the two workflow rows corrected above | ✅ |
