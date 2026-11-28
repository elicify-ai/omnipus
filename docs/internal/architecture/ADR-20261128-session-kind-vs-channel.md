# ADR-20261128-session-kind-vs-channel: A session's kind comes from `type`; `channel` only says where it lives; replies follow the turn's origin

- **Status:** Proposed; founder answered Q1 and Q2 on 2026-11-28 (see the amendment below, which overrides D5, D8, D9, H9 and NFR-1's mitigation where they differ)
- **Date:** 2026-11-28
- **Deciders:** Founder (D5, D8); Chief Architect (the rest)
- **Issue:** #1931 (P0, open) plus the `channel` field clean-up
- **Source read:** `origin/release/v0.1.1` at `d9cfddc19f46` (2026-11-26). Read only: no build, no test run.
- **Repo home:** `/workspace/omnipus/docs/internal/architecture/ADR-20261128-session-kind-vs-channel.md`. Name minted with `scripts/new-adr-id.sh "Session kind vs channel"`; no slug collision in `origin/release/v0.1.1` (checked with `git ls-tree`). Note: numbered ADRs stopped at ADR-096 (ADR-094..096 grandfathered); since 2026-09-27 new ADRs are date-named (`scripts/new-adr-id.sh` header). "ADR-067 is the latest" is out of date.
- **Evidence level (highest used):** 1 (founder input, Discord #team-a 2026-11-28) plus codebase facts

## Summary (plain English)

1. A session already says what kind it is in its `type` field ("main", "scheduled", "task"...). We use that and add no second "is main" flag, because it would only repeat `type`.
2. `channel` will only say where a chat lives (web, Telegram...). New Main, scheduled, verifier and task sessions store it empty; old stored labels like "main" are read as empty.
3. One shared rule in the session package decides "does this chat have an outside address?", replacing four separate guesses. Only real connector chats (type `channel` with a recorded chat id) have one; everything else answers in its own web chat.
4. A reply goes where the message was typed. `/stop-redirect` typed in the web on a Main chat answers in the web; typed in Telegram it answers in Telegram. Plain mid-turn messages still just queue.
5. No data migration job. Old values are cleaned when read, so the live Mac instance needs no extra step and rolling back stays safe.

> ⚠️ **Correction (2026-11-28, after the founder's answers and spec grill round 1):** points 2 and 5 above are superseded. Old labels are **not** cleaned on read: the founder chose a one-time script for the live instance (Q1). A rollback after the new build has run installs the previous binary only; backups are restored only before the instance runs again. A Main redirect typed in a connector is answered through the existing captured-request mechanism (D3a below; the Main check sits inside admission per amendment 2026-11-28b). Impact: spec FR-011, FR-019..FR-030.

## Amendment 2026-11-28 (founder Q1/Q2 answers; spec grill round 1)

Source: founder answers 2026-11-28 06:03Z (as applied in `/workspace/logs/1931/spec/spec.md`); grill report `/workspace/logs/1931/spec/grill-round1.md`. Code re-read at `origin/release/v0.1.1` `ef6845a5d`.

| Item | Was | Now |
|---|---|---|
| D5 (old labels) | II-B clean on read | **II-C as a one-time offline script for the live instance only** (founder Q1): dry run, backup, apply, second dry run, before the new build starts. No read-time clean-up, no rewrite on save, no boot step in the product. Partial apply failure exits 3 and is finished by an idempotent re-run (spec FR-030) |
| D8 (PW-D1) | "legacy `main` is read as empty" | "old labels were cleared once by the #1931 script, the one sanctioned rewrite; a wake never rewrites metadata" (spec FR-018) |
| D3 web-view corner | Open (Q2) | Keep today's behaviour (founder Q2) |
| **D3a (new)** Main redirect typed in a connector | D3 said only "answers in Telegram", resting on route selection | Route selection alone does not deliver: a Main refuses every plain connector send by design (`connector_egress.go:16-31`, `loop_publish.go:51-55`), and the redirect continuation carries a `SessionID`, so bound-connector admission skips it and no capture exists to answer (`connector_intake.go:49`). Decision: after the stop, `continueOrdinaryAfterStop` admits the instruction through the existing `admitBoundConnectorInputCtx` (capture, transcript entry, envelope), checked first against the bound pair's Main; the agent answers that request through the existing answer path. The default-send refusal and `pkg/channels` stay unchanged (spec FR-028). Rejected: relaxing the refusal for redirects (re-opens the mixed-sender leak it closes); passing the full envelope through the channel-control interface (cross-package change for a thread id and display name only) |
| NFR-1 / D5 rollback | "the old build reads `""` as web chat" (still true) | Rollback after the new build has run = previous binary only, no restore: restoring whole identity files would lose later title/model/effort/partition/owner changes (`unified_meta_files.go:66-94`). Backup restore only before the instance runs again (spec FR-027, FR-029) |
| D9 tests | read-time clean-up tests; `parent_wake_session_red_test.go:70` rewritten | No read-time tests; `parent_wake_session_red_test.go` unchanged; only `parent_wake_channel_red_test.go` line 68 ("webchat peer" row) changes; the R1 refusal test is deleted (D2(b)); Main connector redirect proven by spec T-08/T-19 with the real bound fixture |
| H9 | "roll back to the previous build" | Previous binary only, no restore, against a script-cleaned legacy session and a new-build session, checking later changes survive (spec HO-9) |
| User docs | none planned | One paragraph in `docs/using-omnipus-ui.md` "Stop and redirect commands" (spec FR-031) |

Confidence: D3a High that it is needed (code read); Medium-high that it works end to end until T-08 runs. Everything else in this ADR stands.

### Amendment 2026-11-28b (spec grill round 2)

Source: `/workspace/logs/1931/spec/grill-round2.md`; changes in `/workspace/logs/1931/spec/fix-round2-changelog.md`. Code re-read at `ef6845a5d`.

| Item | Was (2026-11-28) | Now |
|---|---|---|
| D3a: where the Main check runs | "checked first against the bound pair's Main" (a separate config read before admission) | ⚠️ Correction: a separate read is a different config snapshot (`connector_intake.go::boundConnectorPair` reads one per call; `loop_config.go::SwapConfig` can swap in between), so a rebind could send the instruction to another Main. The expected Main is now checked **inside** the admission, on the pair from admission's own snapshot, before any write: `admitBoundConnectorInputCtx`'s body moves into `admitBoundConnectorInputForMain(ctx, msg, wantMainID)`, and the old name calls it with `""` (behaviour unchanged for normal intake). Unbound with a pinned Main is a refusal, not a no-op. `connector_intake.go` may therefore change by exactly that (spec SC-008). Rejected: a second copy of the intake logic in the redirect code (two security paths to keep in step) |
| D3a: captured but not started | not defined | If the turn cannot start after a successful capture, the request is withdrawn with admission's own mechanism (`settleRequest` + `Ledger.Discard`), the transcript keeps the instruction, and the chat gets the not-applied notice with curated text and the raw instruction; a failed withdrawal is logged and said in the notice (spec FR-028 (a3), (a4)) |
| D5 rollout with an unparseable session file | Spec allowed starting after exit 1 | ⚠️ Correction: boot refuses a present unparseable identity file on any build (`unified_conv.go::convNormalizeSavedChatDirs`), so exit 1 stops the rollout until that file is resolved outside this change (spec FR-026). No product change |

---

## 1. Problem Understanding

**Bug (#1931).** A `/stop-redirect` in any Main chat is refused ("Not stopped. Press Stop again."). `[FACT]` `pkg/agent/stop_redirect_root.go:110-128` (`ordinaryRedirectRoute`) treats a session as web chat only when its stored `channel` is `""` or `"webchat"`. Main stores `"main"` (`pkg/session/unified.go:812`) and has no `PeerID` (the outside chat id), so the function returns "is a main chat with no recorded chat id".

**Root cause: one field, two meanings.** `[FACT]` The stored `channel` holds either a place (`"webchat"`, `"telegram"`, set by `NewChannelSession`, `unified.go:634-640`) or a kind label:

| Label | Writer | Type it goes with |
|---|---|---|
| `"main"` | `unified.go:812` `createMainSessionLocked` | `main` |
| `"scheduled"` | `unified.go:738` `NewScheduledSession`, `unified.go:886` `GetOrCreateScheduledSession` | `scheduled` |
| `"verifier"` | `unified.go:849` `NewVerifierSession` | `verifier` |
| `"system"` | `pkg/agent/task_executor.go:845`, `pkg/agent/plan_engine.go:2285` | `task` |
| `"heartbeat"` | retired writer (commit `48547a024^`, per `/workspace/logs/1931/architect-review.md`); no live writer found | `heartbeat` (still a valid type, `unified.go:82`) |

`[FACT]` The kind is already stored separately in `type` (`unified.go:20-70`, `u5IdentityFile.Type`, `pkg/session/unified_meta_files.go`). `"main"` in `channel` was not a design decision; it copied the heartbeat habit (architect review section 2, commit `48547a024`).

**Four places guess "is this an outside chat?" in four different ways** `[FACT]`:

| Site | Today's guess |
|---|---|
| `pkg/agent/stop_redirect_root.go:119-127` `ordinaryRedirectRoute` | `channel` is `""`/`"webchat"` → web; else needs PeerID or errors (the bug) |
| `pkg/agent/wake_address.go:8-12` `wakeStreamAddress` | `channel` is `"main"`/`""` or no PeerID → web |
| `pkg/agent/steer_launcher.go:657-661` `reportingTargetFor` | `channel` set and PeerID set → outside; else self (`steerReportingSelfChannel = "webchat"`, line 630) |
| `pkg/agent/loop_session.go:62` `rebuildChannelSessionIndex` | `channel` `""`/`"webchat"` or no PeerID → skip |

**Who is affected.** Everyone using Main (the chat most users live in), plus scheduled and task chats `[INFERENCE, medium-high]`: they also store a label and no PeerID, so `ordinaryRedirectRoute` refuses them the same way (code reading, not run).

## 2. Extracted Requirements

### Functional (founder, 2026-11-28, Discord #team-a, unless tagged otherwise)
- **FR-1** Main is the agent's default session, one per workspace+agent. It is not a channel. `[FACT, founder]`
- **FR-2** Main can be reached from the web, Telegram or other connectors; which one does not matter. `[FACT]` `pkg/agent/connector_intake.go` routes a bound connector's messages into Main (header A1-1..A1-6; the return route comes "only from the inbound envelope").
- **FR-3** The session's place (`channel`) and its kind must be separated. `[FACT, founder]`
- **FR-4** A reply goes where the message came from (the turn's origin), never to a label stored on the session. `[FACT, founder]`
- **FR-5** Main has no channel (empty). `[FACT, founder]`
- **FR-6** `/stop-redirect` on a Main chat replies where it was typed. `[FACT, founder]`
- **FR-7** New sessions stop writing kind labels into `channel`. `[FACT, founder]`
- **FR-8** Old stored data is read correctly. `[FACT, founder]`
- **FR-9** Plain messages sent mid-turn queue and cancel nothing (steering queue). Only the explicit `/stop-redirect` command is in scope. `[FACT, founder]`
- **FR-10** Scheduled, verifier and task chats behave like Main for FR-5..FR-8 (no kind label in `channel`; web redirect works). `[INFERENCE]` from FR-3/FR-7 ("new sessions stop writing kind labels").

### Non-functional
- **NFR-1 Rollback safety:** installing the previous build after this lands must not break routing for sessions touched by the new build. `[EXPERT REASONING]` (live Mac instance is updated after each landing).
- **NFR-2 No new moving parts:** no new field, enum, registry or migration framework without a present need (Chief Architect simplicity rule).
- **NFR-3 Contract honesty:** the REST contract text must match what the server returns.
- Performance: no target given; the change adds a string comparison per read `[INFERENCE]`, negligible.

### Constraints
- `channel` stays a free string on the wire (no enum today: `contracts/components/schemas/Session.yaml:193-197`). `[FACT]`
- `contracts/components/schemas/Session.yaml` and `pkg/gateway/inboundschemas/Session.yaml` are identical copies (`diff` returned no difference). `[FACT]` Both must change together; the backend lead edits contract files and regenerates.
- `readUnifiedMeta` is the only reader of the stored session files (FR-060 comment, `pkg/session/unified.go:1012-1040`). `[FACT]`
- Earlier "no migration, no back-compat" stance (ADR-057 v4 operator decision 1, restated by ADR-086 D-F; quoted in `unified.go:1033-1034`). FR-8 is the founder's explicit exception for this field. `[FACT]` for the quote, `[INFERENCE]` that FR-8 overrides it here.

## 3. Gaps and Ambiguities

| # | What's missing/ambiguous | Why it matters | Likely assumption if unresolved | Question to resolve |
|---|---|---|---|---|
| G1 | A `/stop-redirect` typed in the **web** view of a **Telegram** conversation (type `channel`): continue on Telegram (today, deliberate per the `ordinaryRedirectRoute` doc comment) or in the web where it was typed (FR-4 read literally)? | FR-4 and the existing redirect spec point different ways for connector chats | Keep today's behaviour; this ADR changes only non-connector chats | Founder Q2 (section 9) |
| G2 | A delegation started by a Main turn that came from Telegram reports back to Main's own web chat (`reportingTargetFor` reads session metadata, not the launching turn's origin) | FR-4 says "origin of the turn"; whether the Telegram requester still gets the answer depends on the connector request ledger (`connector_intake.go` A1-5) | Out of scope for #1931; behaviour unchanged by this ADR | ❓ Unknown: settle with one hands-on test (H6) before deciding a follow-up |
| G3 | How many sessions on the live Mac instance carry legacy labels | Sizes option C's work | Not needed for the recommended option | ❓ Count `meta.json` files per label on the Mac (Mac squad), only if C is chosen |
| G4 | Whether any test asserts byte-identical re-marshalling of a stored `meta.json` with a legacy label (FR-057/AC-21(e) mention byte-identical round-trip) | D5 changes the in-memory value of legacy labels | No such test found by a quick grep (hits were chat-history files) | Developer runs the `pkg/session` targeted tests at checkpoint |
| G5 | Owner of `pkg/agent/`, `pkg/gateway/`, `contracts/` | `teams.yaml` lists no owner for them (Team B owns `pkg/session/`, `src/`; Team A owns tool/task areas) | PM assigns | PM |
| G6 | Whether `sessioncore_u1_*` fixture tests read `channel` after seeding `"main"` | Decides whether they need edits | They test owner/workspace guards, not channel (their comments say so) | Developer checks at test-writing time |

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Fixes #1931 and the same bug in scheduled/task chats | High | P0 |
| Meets founder FR-3..FR-8 ("done properly") | High | |
| Simplest: fewest new parts, reuses `type` | High | NFR-2 |
| Rollback safety and live-Mac cost | Medium | NFR-1 |
| Wire/contract clarity for clients and the Test Lab | Medium | NFR-3 |

## 5. Option Analysis

Two sub-decisions need options: **(I) how to mark "Main / kind"**, and **(II) what to do with old stored labels**. The shared rule, the write side and the contract text follow from them.

### (I) Marking the kind

**Option I-A: use the existing `type` (recommended)**

| Dimension | Assessment |
|---|---|
| Strengths | Already stored on every session and required (`Session.yaml` "a missing type is invalid"); already on the wire; covers Main, scheduled, verifier, task, delegate, heartbeat with one test |
| Weaknesses | Callers must learn to ask `type`, not `channel` (enforced by the shared helper) |
| Risks | A future kind added without thought; low, because the rule is "only `type = channel` with a peer is external", so new kinds default to web |
| Complexity | None added |
| Cost | Zero data, zero wire |
| Operational impact | None |

**Option I-B: new boolean `is_main` (founder's first description)**

| Dimension | Assessment |
|---|---|
| Strengths | Reads naturally |
| Weaknesses | Duplicates `type == "main"`; two fields can disagree (`is_main=true, type=chat`), so every reader needs a tie-break; does not cover scheduled/task chats, which have the same bug |
| Risks | Drift between the two fields; needs a write path, a contract field, a frontend type and a back-fill for every existing Main (a migration) |
| Complexity | New stored field + wire field + back-fill |
| Cost | Contract change, regeneration, back-fill on live Mac |
| Operational impact | Back-fill step |

**Rejected:** I-B. It adds a second source of truth for a fact `type` already holds and fixes fewer cases. I recommend `type` plainly.

### (II) Old stored labels

**Option II-A: read-time tolerance only.** Helper keyed on `type`; stored and wire values of old sessions stay `"main"`, `"scheduled"`...

| Dimension | Assessment |
|---|---|
| Strengths | Smallest change; disk never touched |
| Weaknesses | Main sessions are permanent (one standing session per pair, `unified.go:28-36`), so every existing install shows `channel: "main"` for Main forever, while new installs show `""`. The API keeps two meanings, which is what FR-3 asks to end |
| Risks | Clients or tests later branch on the label again |
| Complexity | Lowest |
| Cost | None on the Mac |
| Operational impact | None |

**Option II-B: clean on read, no migration job (recommended).** In `readUnifiedMeta`, a closed list of five legacy labels (`main`, `scheduled`, `verifier`, `system`, `heartbeat`) on a session whose `type` is not `channel` is returned as `""`. Nothing new writes them. Disk converges by itself the next time that session's identity file is written for another reason.

| Dimension | Assessment |
|---|---|
| Strengths | Engine, REST and frontend see one meaning everywhere, at once, on every install; one place (`readUnifiedMeta` is the only reader); no ops step |
| Weaknesses | Stored file and in-memory value differ until the next identity write; the five-label list is history that must stay in code |
| Risks | A test asserting byte-identical round-trip of a legacy file (G4); the quiet write-back is a stored-identity change PW-D1 currently forbids (needs D8) |
| Complexity | About one small function plus a table test `[INFERENCE]` |
| Cost | No Mac work beyond the normal update |
| Operational impact | Rollback safe: the previous build treats `""` as web chat at all four sites `[FACT]` (lines cited in section 1) — it even fixes #1931 for a Main that was rewritten |

**Option II-C: one-time disk migration (architect review option c)**

| Dimension | Assessment |
|---|---|
| Strengths | Disk and memory identical immediately |
| Weaknesses | Code that runs once, its own tests, a boot step, idempotency and partial-failure handling on the identity write path (`u5WriteIdentityLocked`); still needs II-A/II-B tolerance for a half-run |
| Risks | Touches every legacy session's identity file on live data in one go; a bug hits all of them |
| Complexity | Highest |
| Cost | On the live Mac: stop the instance, back up the sessions folder, run, check, restart (Mac squad), plus any other installs. Session counts ❓ (G3) |
| Operational impact | Ops step per install; rollback needs the backup or relies on old code tolerating `""` (it does) |

**Option II-D: special-case `"main"` only** (founder's earlier rule `Type==main OR channel ""/webchat`). Rejected: leaves scheduled/task/verifier chats broken and keeps the label in data.

## 6. Recommended Architecture

### Decisions

**D1. The kind of a session comes only from `type`. No new `is_main` boolean.**
CONFIDENCE: High
  Basis         : `type` already exists, is required, is on the wire, and has `main`.
  Evidence      : `pkg/session/unified.go:20-70,82`; `contracts/components/schemas/Session.yaml` type enum; only `NewChannelSession` sets PeerID (`git grep` of PeerID writers: `unified.go:640` only, plus file copy code in `unified_meta_files.go`).
  Missing       : None material.
  Would change my mind : a real case where one session must be "main" and another kind at once.

**D2. One shared rule in `pkg/session`: a session has an outside reply address only when `type == channel` AND `PeerID != ""` AND `channel` is not `""` or `"webchat"`. The address is (`channel`, `PeerID`, `InstanceID`). Every other session answers in its own web chat (`"webchat"`, session id).** It replaces the four guesses: `ordinaryRedirectRoute`, `wakeStreamAddress`, `reportingTargetFor`, `rebuildChannelSessionIndex`. Shape (a method on `UnifiedMeta` returning address + ok) is left to the spec.
CONFIDENCE: High for the rule; Medium for the `"webchat"`-with-peer corner
  Basis         : Only connector chats ever get a PeerID (`NewChannelSession`, called only from `pkg/agent/loop_session.go:119` with non-empty channel and chat id). A REST-created `type: channel` session stores `"webchat"` and no PeerID (`pkg/gateway/rest_sessions.go:933-934,992`), so it stays web, as today.
  Evidence      : the four sites read in this run.
  Missing       : Whether a `type: channel` session with `channel "webchat"` and a PeerID exists in real data. Only a test builds one (`pkg/agent/parent_wake_channel_red_test.go`, row "webchat peer", today expects stream to `peer-web`). Under D2 it goes to the session's own web chat; that test row changes.
  Would change my mind : real data with webchat+peer sessions that must stream to the peer id.
  Behaviour changes: (a) Main/scheduled/task/verifier redirect from the web now works (the fix). (b) A corrupt connector session with no PeerID (possible if a crash lands between `NewSession` and the identity write in `NewChannelSession`, `unified.go:635-646`) is redirected to its web chat instead of refused; that is where the command was typed, so it meets FR-4.

**D3. The turn's stated origin wins; session metadata is only the fallback when the trigger states none.** `ordinaryRedirectRoute` already uses the channel the command was typed in when one is stated (`stop_redirect_root.go:112-117`). So `/stop-redirect` on Main typed in Telegram answers in Telegram (route only; delivery for a Main needs D3a in the 2026-11-28 amendment); typed in the web (no stated channel) it falls to D2 and answers in the web. No change for connector chats redirected from the web view (G1, founder Q2). Plain mid-turn messages are untouched (FR-9).
CONFIDENCE: Medium
  Basis         : code path read; web frames carry `Channel: "webchat"` (`pkg/gateway/websocket_chat.go:1149`, from the architect review).
  Missing       : hands-on proof for the Telegram-typed case on Main (H2); G1 answer; G2 (delegation report route).
  Would change my mind : founder answers Q2 "web, where typed" for connector chats too; then `ordinaryRedirectRoute` drops its metadata branch for web-typed commands entirely (simpler).

**D4. Write side: kind-only sessions store `channel = ""`.** Change `createMainSessionLocked` (`unified.go:812`), `NewScheduledSession` (`unified.go:738`), `GetOrCreateScheduledSession` (`unified.go:886`), `NewVerifierSession` (`unified.go:849`), task sessions in `task_executor.go:845` and `plan_engine.go:2285`. Unchanged: connector sessions (real channel), chats and tasks created from the web/REST (`"webchat"`, `rest_sessions.go:990-992`, `websocket_chat.go:902-904`; that is where they were made), delegates (already `""`, `steer_launcher.go:370,565`).
CONFIDENCE: High
  Basis         : FR-5, FR-7; no non-test code compares the stored channel to these labels except `wake_address.go:9` (`git grep '(==|!=) *"(scheduled|verifier|heartbeat|main)"'`).
  Missing       : None material. Note: the message-level channel `"system"` on the bus (`constants.IsInternalChannel`, `goal_triggers.go:2236`, `connector_egress.go:40`) is a different thing and is NOT changed.
  Would change my mind : a reader of stored `channel == "scheduled"` found outside `pkg/` (not searched: only `pkg/`, `cmd/`, `src/`).

**D5. Old data: option II-B, clean on read; no migration job.** In `readUnifiedMeta`, when `type != channel` and `channel` is one of `main`, `scheduled`, `verifier`, `system`, `heartbeat`, return `""`. The list is closed (nothing writes these after D4).
CONFIDENCE: Medium
  Basis         : Main is permanent, so II-A leaves the old meaning on the wire forever on existing installs; II-C costs an ops step and one-off code for the same visible result.
  Evidence      : single reader (`unified.go:1012-1040`); old code treats `""` as web at all four sites (rollback safe).
  Missing       : G4 (byte-identical round-trip test); founder's yes that a quiet write-back is acceptable (PW-D1 says "do not rewrite the stored channel").
  Would change my mind : founder prefers "never touch stored identity" → take II-A (helper still fixes all routing; contract text then says legacy labels may appear). II-C only if some outside tool reads `meta.json` directly (none known).
  Live Mac cost: II-B none beyond the normal update; II-A none; II-C stop, backup, run, verify, restart, per install.

**D6. Contract text, no shape change.** In both identical copies of `Session.yaml` (`contracts/components/schemas/` and `pkg/gateway/inboundschemas/`), `channel` description becomes, in substance: "Where the conversation lives: the connector channel of a `type: channel` session (e.g. "telegram"), "webchat" for chats created in the web app, empty for sessions that are not bound to one place (main, scheduled, verifier, task created by the engine, delegate). Does not say what kind the session is: use `type`." Keep it required, a free string, no enum. `SessionCreateRequest.yaml` has no `channel` property (its fields are `agent_id`, `type`, `workspace_id`, `new_chat_key`); only its comment about server-minted types may gain "verifier, delegate" — optional. `rest_sessions.go:247` stays a pass-through. Backend lead edits and regenerates.
CONFIDENCE: High
  Would change my mind : a client found that needs an enum.

**D7. Frontend: no logic change.** `src/lib/api/sessions.ts` only maps `channel` (lines 64, 132, 172); no branch on a session's `channel` found in `src/` (non-test). Optional: fix the comment at `sessions.ts:62-63` ("treat undefined as webchat") to match D6.
CONFIDENCE: High (grep), not proven for dynamic property access.

**D8. Amend spec PW-D1 with a dated correction.** `/workspace/omnipus/docs/internal/specs/parent-wake-visibility-spec.md` lines 18, 54, 133, 190-191, 268, 272, 321, 387 and the scenario at 574-581 (and its trace row at 1374) "A web wake does not rewrite the stored channel": restate as "Main stores no channel; a legacy stored `main` is read as empty; a wake never rewrites metadata itself." The rule "the woken turn streams in the open web chat" (the actual PW-D1 decision) stays. PW-D1's source is the founder interview of 2026-10-15 (spec line 14), so the correction needs his yes (founder Q1).
CONFIDENCE: High that it is needed; the wording is the spec author's.

**D9. Tests.** Change: `pkg/agent/parent_wake_channel_red_test.go:65` keep the `"main"` row as a legacy row (still web), adjust the `"webchat peer"` row per D2; `pkg/agent/parent_wake_session_red_test.go:70` (asserts `after.Channel == "main"`) becomes "the wake did not write meta.json" (compare file bytes) plus "read value is empty"; `pkg/session/sessioncore_u1_newsymbol_test.go:121` and `pkg/gateway/sessioncore_u1_identity_refusal_test.go:45` keep `"main"` as legacy fixtures unless they assert channel (G6). Add: helper table (each `type` × each legacy label × peer present/absent × webchat); web-typed redirect in Main, scheduled, task, verifier chats continues in web; connector chat redirected from web continues on its channel and peer (unchanged); Main redirect typed in a connector answers in that connector; creators write `""`; `readUnifiedMeta` returns `""` for the five labels and leaves real channels and `"webchat"` alone; GET `/api/v1/sessions` shows `channel: ""` for Main; rollback reasoning test: a session stored with `""` routes to web through each of the four old guard expressions (assert the predicate, since the old binary cannot run in a unit test).
CONFIDENCE: High

**D10. One work package, one owner pair.** The helper, the write side and the tests touch the same files (`pkg/agent` wake/redirect tests, `pkg/session/unified.go`). Splitting would edit the same tests twice. If the founder's answer on Q1 is late, land D1-D4 + D9 first (they fix the P0 and need no contract or spec change beyond PW-D1's test rows), then D5-D8.
CONFIDENCE: Medium
  Would change my mind : P0 pressure; then land D2 alone first.

### Rejected, one line each
- New `is_main` boolean: duplicates `type`, misses scheduled/task.
- Special-case `"main"` only: leaves the same bug in scheduled/task/verifier chats.
- Disk migration job (II-C): ops step and one-off code for the same visible result as II-B.
- Channel enum or registry: no client needs it (NFR-2).

### Files to change

| File | Change | Owner (teams.yaml) |
|---|---|---|
| `/workspace/omnipus/pkg/session/unified.go` | D2 helper on `UnifiedMeta`; D4 at lines 738, 812, 849, 886 | Team B |
| `/workspace/omnipus/pkg/session/unified.go` (`readUnifiedMeta` ~1040) or `unified_meta_files.go` (`u5ComposeUnifiedMeta`) | D5 clean-on-read | Team B |
| `/workspace/omnipus/pkg/agent/stop_redirect_root.go` | use helper in `ordinaryRedirectRoute` | ❓ unassigned (G5) |
| `/workspace/omnipus/pkg/agent/wake_address.go` | use helper (file may shrink to a call) | ❓ |
| `/workspace/omnipus/pkg/agent/steer_launcher.go` | `reportingTargetFor` uses helper | ❓ |
| `/workspace/omnipus/pkg/agent/loop_session.go` | `rebuildChannelSessionIndex` uses helper | ❓ |
| `/workspace/omnipus/pkg/agent/task_executor.go`, `/workspace/omnipus/pkg/agent/plan_engine.go` | D4 `"system"` → `""` | ❓ (Team A owns `pkg/task/`, `pkg/plan/`, not these files) |
| `/workspace/omnipus/contracts/components/schemas/Session.yaml`, `/workspace/omnipus/pkg/gateway/inboundschemas/Session.yaml` (+ regenerated types) | D6 text | Backend lead |
| `/workspace/omnipus/src/lib/api/sessions.ts` | optional comment | Team B |
| `/workspace/omnipus/docs/internal/specs/parent-wake-visibility-spec.md` | D8 dated correction | spec owner, after founder yes |
| Tests listed in D9 | D9 | test author (not the developer) |

## 7. Risks and Caveats

| Risk | Mitigation |
|---|---|
| A reader outside `pkg/`, `cmd/`, `src/` (scripts, Test Lab checks) relies on `channel: "main"` | Test Lab and Team B are told before landing; Team B's #1931 note read the API, so their checks may assert it |
| G4 byte-identical round-trip test breaks under D5 | Run `pkg/session` targeted tests at checkpoint; if one exists, it covers fixture files, adjust to the new rule |
| Quiet write-back (D5) surprises someone debugging disk files | State it in the code comment and the contract text; rollback is safe either way |
| G1: connector chat redirected from the web still continues on the connector | Explicitly out of scope; founder Q2 |
| G2: delegation results from a Telegram-started Main turn report to the web | Unchanged by this ADR; hands-on H6 tells us if it is a real gap |
| One-way doors | None. Every step is reversible: the old build reads `""` as web chat |

## 8. Confidence Assessment

| Decision | Confidence |
|---|---|
| D1 use `type`, no boolean | High |
| D2 shared rule | High (Medium on the webchat+peer corner) |
| D3 origin wins, connector-from-web unchanged | Medium |
| D4 write `""` for kind-only sessions | High |
| D5 clean on read, no migration | Medium (needs founder yes) |
| D6 contract text only | High |
| D7 frontend no logic change | High (grep) |
| D8 amend PW-D1 | High that it is needed (needs founder yes) |
| D9 tests | High |
| D10 one work package | Medium |

## 9. Validation / Next Steps

**Hands-on tests (agent engine is touched: two independent testers, on different models from the developer and from each other; a Fly test instance seeded with a copy of legacy-style sessions):**

| # | Check |
|---|---|
| H1 | Main chat in the web: start a long turn, type `/stop-redirect <text>`: the turn stops and the new instruction is answered in the same web chat (no "Not stopped") |
| H2 | Main reached through a bound Telegram (or other connector) instance: `/stop-redirect` typed in Telegram is answered in Telegram |
| H3 | Same as H1 in a scheduled chat and in a task chat |
| H4 | A Telegram conversation (type `channel`) redirected from its web view still continues on Telegram (unchanged behaviour) |
| H5 | Plain message sent mid-turn in Main queues and cancels nothing |
| H6 | Main via Telegram delegates to a helper: where does the result appear, and does the Telegram requester get an answer? (settles G2) |
| H7 | GET `/api/v1/sessions`: Main shows `channel: ""` on a fresh instance and on the legacy-seeded one; connector sessions keep their channel |
| H8 | Woken parent (PW-D1) still streams live in the open Main web chat |
| H9 | Roll back to the previous build on the test instance (binary only, no backup restored; see amendment): H1 and H8 still route to the web chat (no crash, no refusal), later changes survive |

**Founder questions (one line each):**
- **Q1** Old stored labels: **A** (recommended) clean them when read, no migration job; the stored file is rewritten quietly the next time that session's details are saved; amend PW-D1 accordingly. **B** never touch stored data; the API keeps showing `"main"` for existing Main chats forever, routing still correct. **C** a one-time migration of every install (stop, backup, run, restart on the Mac).
- **Q2** A redirect typed in the web view of a Telegram conversation: **A** (recommended for now) keep continuing on Telegram as today. **B** answer in the web, where it was typed.

**Actions:**
- PM: assign an owner for `pkg/agent/` and `pkg/gateway/` files (G5) and plan one work package for #1931 (D10).
- Team B lead: `pkg/session` part (D2 helper, D4, D5) on the #1931 work package.
- Backend lead: D6 contract text in both `Session.yaml` copies and regenerate, after Q1.
- Spec owner of parent-wake: D8 dated correction after Q1.
- Chief Architect: ruling file in `/workspace/omnipus-coordination/decisions/` after Q1/Q2; commit this ADR to `/workspace/omnipus/docs/internal/architecture/ADR-20261128-session-kind-vs-channel.md` on a documents branch.

**Handoff:** `/grill-spec /workspace/logs/1931/adr-session-kind-vs-channel.md`, then `/plan-spec` for #1931 from it.

## Evidence

| Claim | Evidence | Label |
|---|---|---|
| Redirect refuses non-webchat channel without PeerID | `git show origin/release/v0.1.1:pkg/agent/stop_redirect_root.go` lines 110-128 | ✅ |
| Wake rule | `pkg/agent/wake_address.go:8-12` | ✅ |
| Report target rule and self channel | `pkg/agent/steer_launcher.go:630,657-661` | ✅ |
| Index rebuild rule | `pkg/agent/loop_session.go:62` | ✅ |
| Label writers | `git grep` of session creators: `unified.go:738,812,849,886`, `task_executor.go:845`, `plan_engine.go:2285` | ✅ |
| Web/REST chats store "webchat"; REST allows type channel | `rest_sessions.go:933-934,990-992`; `websocket_chat.go:902-904` | ✅ |
| Only `NewChannelSession` sets PeerID | `git grep 'PeerID *(=|:)'` non-test | ✅ |
| No non-test code compares stored channel to kind labels except wake_address | `git grep -nE '(==|!=) *"(scheduled|verifier|heartbeat|main)"'` | ✅ |
| Frontend does not branch on session channel | grep of `src/` non-test | ✅ grep / 🔶 dynamic access |
| Contract copies identical; no channel in create request | `diff` of both `Session.yaml`; field list of `SessionCreateRequest.yaml` | ✅ |
| Single meta reader; prior no-migration stance | `unified.go:1012-1040` | ✅ |
| PW-D1 text and founder-interview source | `parent-wake-visibility-spec.md:14,18,576-581` | ✅ |
| Tests pinning "main" | `parent_wake_channel_red_test.go:65`, `parent_wake_session_red_test.go:70`, `sessioncore_u1_newsymbol_test.go:121`, `sessioncore_u1_identity_refusal_test.go:45` | ✅ |
| ADR naming scheme and id | `scripts/new-adr-id.sh` header and output; `git ls-tree` collision check | ✅ |
| Scheduled/task redirect also refused today | code reading only | 🔶 medium-high |
| Owners of pkg/agent, pkg/gateway | `teams.yaml` lists none | ❓ |
| Live Mac session counts | not checked | ❓ |
| Self-check | Re-read every cited line in this run; no build or test run; no external links cited | ✅ |
