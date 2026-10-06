# Cross-Channel `/cancel` Command Spec — v2 (Revised)

## Amended 2026-10-06 — founder decision

**Amended 2026-10-06 (founder):** Stop click 1 / Esc 1 / `/stop` stops **this chat's current turn only** and opens a **3 s window**. A second Stop / Esc / `/stop` within it stops **this chat and its whole helper tree**; `/cancel` does that immediately. **No separate button or offer.** `/stop-redirect <instruction>` stops this chat's turn and continues **this chat** with the instruction, in **any root or helper chat**. Same semantics on web, CLI and channels. Plain Stop leaves background shells running; a second Stop / Esc within 3 s, or `/cancel`, kills them. Agent delegate `stop` / `stop_all` is unchanged (one helper's turn / its tree). These are distinct commands, not aliases. Authority: founder decision, 2026-10-06.

Every person/agent trigger uses **`AgentLoop.StopSession`**. Polite immediately; forced **3 s** later; detach any selected execution still running **3 s after force** (6 s from the initial request if both stages run). No caller-selectable `hard`, no agent 5-second grace, no `session_messaging.cancel_grace`. Internal hard-abort stage labels are not a public hard option. Only the producing execution settles stopped after its work/tail shuts down; visible errors distinguish accepted/pending from landed. Goals remain active.

**Reply expectations:** a plain Stop reply must describe **this turn/session only**, never say helpers stopped. A reply to a request to stop the chat and its helper tree names the downward tree scope and any pending/incomplete/unreachable sessions; accepted or reached is not proof of stopped. The old shared-transcript cascade basis is historical: helpers own transcripts; the durable edge defines the tree-scope stop. The original §15 holdout is retained verbatim as historical evaluation material; use these amended scope/timing requirements as the current oracle. Source: [The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume](../architecture/ADR-20260928-sub-agent-control-plane.md)::D2/D7/D9 and LANE-A-ONE-STOP-DECISION-20261005.md (private record).

Status: Draft

**Status:** Draft — revised after `/grill-spec` review on 2026-05-14
**Owner:** Daniel
**Target release:** v0.1 (in-flight bug fix — see Decision #10)
**Original date:** 2026-05-11
**Revision date:** 2026-05-14
**Related:** Issue tracking TBD; supersedes silent-orphan-loop behavior
**Review:** `docs/internal/specs/cancel-cross-channel-spec-review.md`

## Revision summary (v1 → v2)

The v1 spec was BLOCKED by adversarial review with 4 CRITICAL findings, all verified against the codebase. Changes in v2:

- **F-01 fix (CRITICAL):** Added prerequisite step + FR-6a + T0 — sub-turns MUST inherit `TranscriptSessionID` from parent's processOptions, currently not wired in `pkg/agent/subturn.go:435-451`. Without this, the entire cascade matches zero sub-turns.
- **F-02 fix (CRITICAL):** Removed handoff-cascade story (US-4.2 deleted, BDD scenario deleted). Handoff is `sessionActiveAgent.Store()` (`loop.go:3067-3086`) — a routing override applied to the next inbound user message, not a concurrent agent. Nothing is "running" post-handoff that needs cancelling.
- **F-03 fix (CRITICAL):** Introduced new function `InterruptSessionHard(sessionID, hint)` (existing `InterruptHard()` has signature `func() error` operating on `getAnyActiveTurnState()` — it cannot target a session). Spec now lists this as a new symbol, not "reused."
- **F-04 fix (CRITICAL):** Separated transcript-store and context-store concerns. The `{truncated: true}` flag and `{type: "turn_cancelled"}` entry go on `transcript.jsonl` (UI replay, via `AppendTranscript`). The agent's `context.jsonl` (LLM history, via `AddFullMessage`) keeps the partial content untouched — flipping flags there has no semantics for the next turn's LLM context.
- **F-05 fix (MAJOR):** Added FR-3a — SPA `shouldShowSlash` gating must allow `/cancel` during streaming (the only command that should be reachable mid-turn).
- **F-06 fix (MAJOR):** Clarified FR-7 — pending tool approvals are auto-denied; tools that have already started execution are out of scope (their goroutines may persist or self-cancel based on their own context handling).
- **F-07 fix (MAJOR):** Resolved EC-1 vs FR-20 contradiction — `turn_cancel_attempt` audit entry is always written; `turn_cancelled` is written only when cancel actually fired.
- **F-08 fix (MAJOR):** Added FR-25a — abuse-detection observability for cross-user cancel patterns in multi-user channels.
- Minor findings F-09 through F-20 addressed inline.
- All 10 "unasked questions" from review section 5 now answered in §8 or §11.

---

## Escape dismissal and Stop counting

**Amended 2026-10-06 (founder):** Esc handled by a slash/mention menu, dialog or editor dismisses that surface only; it does not count as a Stop press. The first **eligible** Esc then stops this chat's current turn and opens the 3 s window; the next eligible Esc within it stops this chat and its helper tree. This keeps layered dismissal without a separate button.

---

## 1. Context & Motivation

### Glossary

- **Tier A channel** — Platform supports first-class command registration (Telegram BotCommands, Slack Slash Commands, Discord Application Commands, Adaptive Cards). Channel implements `CommandRegistrarCapable`. Native autocomplete UI in the platform.
- **Tier B channel** — Platform requires text-message parsing. No native command UI. Channel handler text-matches `/cancel` on inbound messages.
- **Cascade** — `/cancel` or a second Stop / Esc within 3 s reaches the selected session and its entire helper tree through durable parent edges. Plain Stop reaches only the current turn of the one session, never helpers. **Amended 2026-10-06.**
- **Graceful cancel** — `requestGracefulInterrupt` flag set on `turnState`; loop exits at the next safe checkpoint (after current LLM iteration completes).
- **Hard cancel** — `context.Cancel()` + `hardAbort` flag + LLM provider's HTTP request explicitly cancelled. Aborts mid-stream.
- **Detached/neutered turn** — Sub-state where `turnState.abandoned = true`; all writes/frames/cost-accumulations from that turn become no-ops. Used when the goroutine refuses to exit after hard cancel.

### Channel inventory (post-MaixCam-removal)

Factory IDs match `pkg/channels/manager.go` and the spec uses these names uniformly:

| Tier | Channels (factory IDs) |
|---|---|
| Tier A (7) | `telegram` (existing impl), `slack`, `discord`, `teams`, `feishu`, `dingtalk`, `googlechat` |
| Tier B (9) | `matrix`, `irc`, `line`, `weixin`, `wecom`, `qq`, `onebot`, `whatsapp`, `whatsapp_native` |

MaixCam removed in prep commit (see §16 phase 1).

### The problem

Today the Omnipus agent loop has three structural cancellation gaps:

1. **Sub-turns are not cancelled by `InterruptSession`.** Two reasons compound:
   - `pkg/agent/subturn.go:435-451` builds child `processOptions` without setting `TranscriptSessionID` → every sub-turn in `activeTurnStates` has `ts.transcriptSessionID == ""`.
   - `pkg/agent/steering.go:382 InterruptSession` walks `activeTurnStates` looking for `ts.transcriptSessionID == sessionID` — matches the parent only.
   - Result: web Stop button stops the visible parent turn while subagents keep burning tokens.
2. **No cancel mechanism exists outside the web SPA.** Chat-channel users have no way to stop an in-flight turn. A misbehaving bot in a group chat is uncancellable until it completes naturally.
3. **Cancelled turns leave no audit trail.** `handleCancel` (`pkg/gateway/websocket.go:734`) is silent at audit level. Forensic queries ("who cancelled what when?") have no answer.

These surfaced during v0.1 release verification: orphan agent loops accumulated under Playwright suite load and broke test reliability. CLAUDE.md hard-constraint #7 (release responsibility) requires we fix this before v0.1 ships.

### The feature

A `/cancel` command available across every surface:

- **Web SPA / CLI** — first eligible Stop/Esc or `/stop` stops this chat's turn only and opens 3 s; second Stop/Esc within it stops this chat and its helper tree. `/cancel` is an immediate stop of the chat and its helper tree. No separate button. **Amended 2026-10-06 (founder).**
- **Redirect on every surface** — `/stop-redirect <instruction>` stops and continues the current chat, root or helper; it never redirects a helper selected from the root chat.
- **Chat channels** — `/stop`, `/cancel` and `/stop-redirect <instruction>` keep those same chat-scoped meanings (native commands on Tier A platforms; text parsing on Tier B).

When invoked, `/cancel`:
- **Cascades**, for `/cancel` / a tree-scope stop only, through the selected session's durable helper tree; plain Stop never cascades. Helpers keep their own transcript identities. **Amended 2026-10-06.**
- **Auto-denies** any pending tool approvals on the cancelled turn whose tools have not yet started execution
- **Escalates** from graceful → hard at 3s, then to detach-and-neuter at 6s (3s + 3s) if the goroutine still hasn't exited
- **Aborts the LLM provider's in-flight HTTP request** at graceful (not just at hard) — calls `turnState.providerCancel()` immediately so the 3s window doesn't wait for OpenRouter's stream to drain naturally
- **Marks** the partial assistant entry in `transcript.jsonl` with `{truncated: true}`; writes a separate `{type: "turn_cancelled"}` JSONL entry to `transcript.jsonl`; leaves `context.jsonl` unchanged
- **Emits** an audit event (`turn_cancelled` for fired cancels; `turn_cancel_attempt` for every request including no-ops; `turn_cancel_stuck` if detach fires)
- **Detaches** any goroutine still alive 3s after hard cancel — `turnState.abandoned = true`; subsequent writes/frames/cost-accumulations from that turn become no-ops without affecting any other session

Background shells are left running by plain Stop and killed by a second Stop / Esc within 3 s, or `/cancel`. This does not introduce force-killing of Go tool goroutines. Out of scope: automatic prompt retries, ending/deleting the chat, rate limiting on `/cancel`, and the HMAC chain (shipped with #155).

### Pre-condition: MaixCam channel removal

MaixCam is an IoT-sensor channel that delivers structured JSON telemetry, not chat. It cannot meaningfully support `/cancel`. We remove it in a standalone prep commit at the head of the branch. Per F-15, this is a separable change — it should land first and could be rolled back independently if needed.

---

## 2. Decisions Log (interview summary)

The original discovery decisions are retained except where the dated founder amendments replace them. Current chat-control scope and interaction follow the 2026-10-06 decision above.

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | All channels: Tier A native registration + Tier B text parsing | Orphan-loop pain is *worst* in channels with no UI button |
| 2 | Cascade to descendants (parent + sub-turns) | "Stop everything" matches user mental model; handoff cascade dropped because there is no concurrent post-handoff turn (F-02 verified) |
| 3 | Two-stage: graceful first, hard after 3s (hard-coded) | Matches Unix `kill` → `kill -9`; uses existing `InterruptGraceful` + new `InterruptSessionHard` (per F-03) |
| 4 | In-scope: sub-turns, pending approvals (tool not yet executed). Out: long-running tools (already executing) | Approvals are pre-execution gates; running tools' contexts already plumbed |
| 5 | Channel-scoped + canceller identity in message ("[cancelled by @bob]") | Avoids RBAC scope creep; abuse detection observability added per F-08 |
| 6 | **Amended 2026-10-06 (founder):** repeating `/cancel` is idempotent. A second Stop/Esc within the 3 s window after a plain Stop instead expands scope to the tree-scope stop. | Confirmation selects the tree; it does not request an immediate force stage. |
| 7 | Immediate ack + progress indicator (in-place edit where supported) | Prevents "stop button is broken" perception during the graceful window |
| 8 | Partial assistant with `truncated:true` (in `transcript.jsonl`) + separate `turn_cancelled` JSONL entry (in `transcript.jsonl`) filtered from chat UI; audit log entry | Clean audit trail; `context.jsonl` unchanged so LLM next-turn sees natural truncation |
| 9 | Fresh start after cancel (no retry button, no continue affordance) | Today's behavior already supports next-message |
| 10 | Ship in v0.1 — it's a bug fix found during release testing | Release-responsibility rule (CLAUDE.md #7) |
| 11 | Stuck goroutine: detach and neuter (3s post-hard-cancel) | Go cannot force-kill goroutines; session-isolated zombie is honest middle ground |
| 12 | **Amended 2026-10-06 (founder):** first Stop/Esc or `/stop` stops this chat's turn and opens 3 s; second Stop/Esc within it stops this chat and its helper tree; `/cancel` is an immediate stop of the chat and its helper tree. `/stop-redirect` stops and continues this chat, root or helper, on every surface. No separate button. | Current authority: control-plane D9 and the founder's Stop-controls decision. |
| 13 | Turn-level only (no session-end via `/cancel`) | Different blast radii deserve different commands |
| 14 | No rate limiting on `/cancel` | Q6 + Q11 already make cancel structurally idempotent and resource-cheap; abuse-detection observability added per F-08 |

---

## 3. User Stories

### US-1: Web user cancels in-flight turn via stop button — P0

**As a** web user with a streaming response or running subagents
**I want** the first Stop/Esc or `/stop` to stop only this chat's turn and open 3 s, the second Stop/Esc within it to stop its whole helper tree, and `/cancel` to do that immediately
**so that** I can recover from a runaway turn without waiting for it to complete naturally.

**Why P0:** Without this, orphan loops accumulate and break test reliability. Headline use case.

**Independent test:** Start a long-running turn, press Stop, verify within 5 seconds the turn ends and the input is re-enabled.

**Acceptance scenarios:**
1. **Given** a turn is actively streaming, **When** the user clicks the Stop button, **Then** the turn ends within 5 seconds (P95) and the partial assistant message displays with the `(interrupted)` suffix.
2. **Given** a turn has two helpers executing and background shells, **When** the user clicks Stop once, **Then** only this chat's turn stops, the helpers and background shells keep working, and a 3 s window opens. **When** Stop/Esc is pressed again within it (even if the first turn has already ended), **Then** the tree-scope stop reaches the chat and its whole helper tree and kills their background shells. There is no separate button or offer. **Amended 2026-10-06 (founder).**
3. **Given** the slash menu is showing mid-stream, **When** the user selects `/cancel`, **Then** the same StopSession mechanism runs with **tree** scope, not plain Stop's session scope. **Amended 2026-10-06.**
4. **Given** a turn is streaming, **When** the user presses an eligible Esc, **Then** it has the same effect as the first Stop click and opens 3 s. Esc consumed by a menu/dialog/editor only dismisses that surface; the next eligible Esc is the first Stop, not the tree-scope stop.

### US-2: Chat-channel user cancels via `/cancel` — P0

**As a** chat-channel user (Telegram/Slack/Discord/WhatsApp/etc.)
**I want** to send `/cancel` to halt the agent's in-flight work
**so that** I have parity with web users and can stop a runaway bot from inside the channel.

**Why P0:** Group chats with bots are where cancel is most valuable — no UI button, multiple users affected.

**Independent test:** Start a turn in a Telegram chat, send `/cancel`, verify within 5 seconds the bot's response stops and a "Cancelled by @user" message appears.

**Acceptance scenarios:**
1. **Given** a Tier A channel (e.g., `telegram`), **When** the user types `/`, **Then** the platform's native autocomplete lists `/cancel` with its description.
2. **Given** a Tier A channel and a streaming response, **When** the user selects `/cancel`, **Then** the response stops within 5 seconds and the bot's "⏸ Cancelling..." message is edited to "✓ Cancelled by @user".
3. **Given** a Tier B channel (e.g., `irc`), **When** the user sends a message exactly matching `/cancel` (case-insensitive, leading/trailing whitespace trimmed, whole-message equality), **Then** cancel fires within 5 seconds.
4. **Given** a Tier B channel without `MessageEditor` capability, **When** cancel completes, **Then** the bot sends two messages: "⏸ Cancelling..." then "✓ Cancelled by @user".
5. **Given** a group channel with multiple users, **When** any user issues `/cancel`, **Then** cancel fires regardless of who started the turn, and the audit log records the canceller's identity.

### US-3: CLI user uses the same Stop sequence — P1

**Amended 2026-10-06 (founder):** one eligible Esc or `/stop` stops this chat's current turn only and opens a 3 s window. A second eligible Esc within it stops this chat and its helper tree; `/cancel` stops this chat and its helper tree immediately. `/stop-redirect <instruction>` stops and continues this chat, root or helper. The old 500 ms double-Escape mechanism is removed, not kept as another mode.

**Independent check:** start a chat with a helper and a background shell. One Esc stops only the current turn; the helper and shell remain. A second Esc within 3 s stops the whole helper tree and kills its background shells. After expiry, an Esc is a first press again.

**Acceptance scenarios:**
1. During inference, Esc 1 stops this chat's turn and opens 3 s. Esc 2 within it stops this chat and its whole helper tree, even if Esc 1 has already ended the turn.
2. A second Esc after expiry is a new first press, not a tree stop.
3. Terminal arrow/function-key sequences are not standalone Esc presses and do not count toward Stop. Ordinary input-line editing keeps its own Escape handling.
4. `/cancel` needs no first Esc. `/stop-redirect <instruction>` continues the chat where it was entered, not a helper selected by that chat.

### US-4: Cancel cascades to all sub-turns — P0

**As any** user (web, chat, or CLI)
**I want** `/cancel` to stop not just the visible parent agent but every sub-turn it spawned and any pending tool approval
**so that** **`/cancel` or a second Stop / Esc within 3 s**, not plain Stop, means “stop this session and every helper below it.”

**Why P0:** Core correctness — partial cascade is the failure mode we're fixing.

**Note (post-revision):** Handoff cascade story removed. Handoff is a routing-override applied to the next user message (`sessionActiveAgent.Store` at `loop.go:3067-3086`), not a concurrent agent. There is no "post-handoff agent turn running while the pre-handoff agent's turn is cancelled" — the scenario is structurally impossible.

**Independent test:** Construct a turn where Mia spawns Jim, Jim spawns Max; cancel; verify all three agent loops exit within 5 seconds and no further tokens are consumed.

**Acceptance scenarios:**
1. **Given** session S has three active helpers, each with its own transcript, **When** `/cancel` or a second Stop / Esc within 3 s fires, **Then** the durable helper-tree walk selects all four sessions; plain Stop selects S only. **Amended 2026-10-06.**
2. **Given** a tool approval is pending (policy = `ask`, user has not clicked Allow/Deny, tool has not yet started execution), **When** `/cancel` fires, **Then** the approval is auto-denied with reason "session cancelled" and the approval modal/prompt closes.
3. **Given** a sub-turn has its own pending approval, **When** `/cancel` cascades, **Then** that approval is also auto-denied.
4. **Given** a tool has already started execution (its own goroutine running, holding its own context), **When** `/cancel` fires, **Then** that tool's context is cancelled (per its caller's context wiring) but the cancel feature does NOT terminate its goroutine forcefully — tool authors are responsible for honoring their context.

### US-5: Cancelled turn is auditable — P1

**As an** operator (or auditor — the HMAC chain shipped with #155)
**I want** every cancellation attempt to appear in the audit log with full attribution
**so that** I can answer "who cancelled what when, and what state was in flight."

**Why P1:** Required for the pentest-hardening compliance (shipped with #155) and operational forensics.

**Independent test:** Trigger a cancel, inspect the audit log, verify a `turn_cancel_attempt` entry exists, plus a `turn_cancelled` entry if cancel actually fired.

**Acceptance scenarios:**
1. **Given** any cancel request lands at the gateway, **When** the audit log is queried, **Then** exactly one `event_type: turn_cancel_attempt` entry exists with `was_fired: true|false`.
2. **Given** a cancel actually fired (session had an active turn), **When** the audit log is queried, **Then** exactly one `event_type: turn_cancelled` entry exists with all required fields (session_id, turn_id, cancelled_by_user, cancelled_by_channel, cancel_method, descendants_cancelled, at).
3. **Given** a cascade cancelled 3 sub-turns, **When** the audit entry is read, **Then** `descendants_cancelled: [<3 turn IDs>]` is populated.
4. **Given** cancel escalated from graceful to hard, **When** the audit entry is read, **Then** `cancel_method` records the final method ("hard").
5. **Given** a stuck-goroutine detach fired, **When** the audit log is read, **Then** a separate `event_type: turn_cancel_stuck` WARNING entry exists with `goroutine_age_after_hard_cancel`.

### US-6: Stuck loop degrades gracefully without disrupting other sessions — P1

**As an** operator running multiple concurrent sessions
**I want** a stuck cancel in session A to not affect sessions B, C, D
**so that** a single bad tool can't disrupt the entire gateway.

**Why P1:** Session isolation is a stability invariant; this story enforces it explicitly.

**Independent test:** Spawn a tool that blocks indefinitely, cancel, verify the cancelled session shows "Cancelled" within 6s (3s graceful + 3s detach) and a sibling session continues working normally throughout.

**Acceptance scenarios:**
1. **Given** session A has a goroutine that ignores context cancel, **When** hard-cancel fires and 3s elapse, **Then** `turnState.abandoned = true` on A; subsequent transcript writes, frames, and cost ticks from A are no-ops.
2. **Given** session A is detached, **When** the user sends a new message to session A, **Then** a new turn starts normally and writes succeed (only the abandoned turn's writes are suppressed, not the session's).
3. **Given** session A is detached, **When** session B sends a message, **Then** B is unaffected — runs at full speed, writes its transcript, emits frames, accumulates cost normally.
4. **Given** a session was detached, **When** the audit log is read, **Then** a `turn_cancel_stuck` event records the case for forensic investigation; additionally, `omnipus_abandoned_writes_suppressed_total` metric counts every suppressed write for observability.

### US-7: MaixCam removed cleanly — P2

**As a** code maintainer
**I want** MaixCam removed from the codebase as a separable prep commit
**so that** the per-channel cancel-wiring code does not need to special-case a channel that cannot meaningfully support text commands, AND the prep change can be rolled back independently of the cancel feature.

**Why P2:** Prep work, separable. Per F-15: this lands as a standalone commit at the head of the branch and is reviewable in isolation.

**Independent test:** After the prep commit, `grep -r "maixcam\|MaixCam" --include="*.go" --include="*.ts" --include="*.md" .` returns zero matches (excluding spec/review files); all builds and tests pass.

**Acceptance scenarios:**
1. **Given** the prep commit has landed, **When** the codebase is searched, **Then** no `pkg/channels/maixcam/` directory exists and no references remain.
2. **Given** a config.json from a prior version contains a `maixcam` section, **When** the gateway boots, **Then** the unknown channel section is ignored with a structured warning logged; other channels load normally.
3. **Given** the gateway boots with a legacy config and is later saved (e.g., via config-update endpoint), **When** the new config.json is written, **Then** the `maixcam` section is dropped from disk. Downgrade compatibility is not preserved (v0.1 has not shipped yet, so no in-the-wild configs exist).
4. **Given** the prep commit, **When** `go build ./...` and `go test ./...` run, **Then** all pass.

---

## 4. Edge Cases

| # | Edge case | Expected behavior |
|---|-----------|-------------------|
| EC-1 | `/cancel` arrives for a session with no active turn | Audit log records `turn_cancel_attempt{was_fired: false}`; no `turn_cancelled` entry written; no transcript entry written; in web, Stop button is disabled (not clickable) but `/cancel` slash menu and Escape are still wired (firing them just records an attempt audit entry); in chat channels, no reply message is sent. |
| EC-2 | `/cancel` arrives right as the turn naturally completes (race) | Protected by `turnState.cancelMu sync.Mutex` (FR-13a). Cancel handler takes the mutex, checks `Finish` has not been called via `cancelFired atomic.Bool`, then writes transcript+audit entries. If `Finish` won the race, cancel becomes a no-op — records `turn_cancel_attempt{was_fired: false}` and exits. No "Cancelled by @user" message sent in chat in this case (the turn completed naturally). |
| EC-3 | `/cancel` arrives during session attach/replay | Replay does not register an active turn in `activeTurnStates` → cascade matches zero. Audit log records `turn_cancel_attempt{was_fired: false}`. Replay continues unaffected. |
| EC-4 | Cancel arrives simultaneously from web AND telegram for the same session | Both land on `InterruptSession(sessionID)`. Per FR-13a's cancel mutex: the first acquires it and starts the graceful timer with `cancelFired=true`; the second sees `cancelFired==true` and returns no-op. Audit log records two `turn_cancel_attempt` entries (first `was_fired: true`, second `was_fired: false`) and one `turn_cancelled` entry. |
| EC-5 | Cancel fires on a turn that already completed but the WebSocket hasn't sent the "turn ended" frame yet | Cancel handler walks `activeTurnStates`; the completed turn already removed itself in `Finish`. Cascade matches zero. `turn_cancel_attempt{was_fired: false}`; no `turn_cancelled`. |
| EC-6 | Cancel during the first message of a brand-new session (no JSONL file yet) | `AppendTranscript` creates the file as part of writing the cancellation entry. The transcript file-creation path is already idempotent. |
| EC-7 | User types `/cancel` as a fragment of a larger message (e.g., `/cancel my dinner reservation`) | In Tier A channels: platform-native command parsing only fires on standalone `/cancel`. In Tier B channels: we match the trimmed, lowercased ENTIRE message exactly equal to `/cancel`; substrings do NOT trigger. |
| EC-8 | Cancel of a turn whose only running operation is awaiting a tool approval (tool not started) | Auto-deny the approval with `reason: "session cancelled"`; the turn loop unblocks and exits within 3s (no LLM stream to wait for). Standard cancel completion follows. |
| EC-9 | Stuck-goroutine detach but the goroutine eventually completes naturally | The completion's transcript-write attempt is suppressed by `turnState.abandoned`. Metric `omnipus_abandoned_writes_suppressed_total` increments. The goroutine returns; Go reclaims memory; no user-visible artifact. |
| EC-10 | Channel-side message edit fails (rate-limited by platform) after "Cancelling..." was sent | Two-message fallback: "Cancelling..." stays as-is; new "✓ Cancelled by @user" message follows. Audit log records `channel_edit_failure: true` field. |
| EC-11 | Cancel arrives with an authenticated token whose user ID is unknown (token revoked between issue and handle) | Cancel still fires (channel-scoped per Q5). Audit log records `cancelled_by_user: "<unknown-user>"`. |
| EC-12 | CLI arrow/function-key sequence contains an Escape byte | The sequence is not a standalone Esc press and MUST NOT count as Stop. A standalone eligible Esc 1 stops this turn and opens 3 s; Esc 2 within it stops this chat and its helper tree. No 500 ms double-Escape mode (FR-31a). |
| EC-13 | In-flight LLM stream when cancel fires | At graceful: `turnState.providerCancel()` is called immediately (FR-12a) — aborts the OpenRouter HTTP request mid-stream. The loop detects ctx.Err on its next iteration check. Without this, OpenRouter's stream could take 5-20s to drain naturally, blowing the 3s graceful window. |
| EC-14 | Sub-turn is in middle of its own LLM stream when parent cancel fires | Each sub-turn has its own `providerCancel`. Cascade calls `providerCancel()` on every matching turnState. All in-flight LLM streams abort in parallel. |
| EC-15 | Cancel during stuck cancel — the SPA UI shows "Stopping..." but 3s passed | At 3s graceful expiry, the Stop button label morphs to "Force-stopping..." If 6s passes (hard + 3s detach), it morphs to "Cancelled" (the detach path treats the user-visible cancel as complete). |
| EC-16 | MCP server in-flight RPC when cancel fires | Context cancellation propagates to the MCP RPC client (via the standard `context.Context` wiring in the MCP package). The MCP server may continue computing on its side, but the client stops reading. Operators of expensive MCP servers should be aware. |

---

## 5. Behavioral Contract

- **When** Stop click 1 / Esc 1 / `/stop` is invoked, **the system** stops only this chat's current turn and opens 3 s. A second Stop / Esc / `/stop` within it stops this chat and its whole helper tree; `/cancel` selects that tree immediately. Plain Stop leaves background shells running; a second Stop / Esc within 3 s, or `/cancel`, kills them. No separate button. Agent delegate `stop` / `stop_all` remains unchanged. **Amended 2026-10-06 (founder).**
- **When** the graceful cancel is initiated, **the system** also calls `turnState.providerCancel()` on every cascaded turnState to abort in-flight LLM HTTP streams.
- **When** the graceful cancel has not completed within 3 seconds, **the system** escalates to hard cancel (`InterruptSessionHard(sessionID)` — new API) on all matching turn states.
- **When** the hard cancel has not caused goroutine exit within an additional 3 seconds, **the system** marks the turnState as abandoned; all subsequent writes/frames/cost-accumulations from that turn become no-ops; an audit event records the abandonment.
- **When** the cancel completes (any stage) AND `cancelFired==true` was set, **the system** writes a single `{type: "turn_cancelled"}` JSONL entry to `transcript.jsonl` and a corresponding audit event; the SPA renders the partial assistant content from `transcript.jsonl` with the existing `(interrupted)` label suffix derived from the entry's `{truncated: true}` flag; chat channels emit a "✓ Cancelled by @user" notification.
- **When** `/cancel` is invoked twice within the 3s graceful window, **the system** ignores the second invocation at the cancel-handler level (no duplicate `turn_cancelled` entry; the second attempt still records a `turn_cancel_attempt{was_fired: false}` audit entry).
- **When** `/cancel` is invoked while this chat has no active turn, **the system** still applies its tree scope to any helpers and background shells. During the 3 s window the existing Stop remains usable for the second press even if the first has already ended this turn; outside that window its ordinary no-turn behavior applies.
- **When** any session in the gateway is cancelled, **the system** must not affect any other session's transcript writes, WebSocket frames, cost accounting, or tool approvals.

---

## 6. Explicit Non-Behaviors

- **The system must not** kill background shells on plain Stop or passive disconnection. a second Stop / Esc within 3 s, or `/cancel` MUST kill background shells in the selected chat and helper tree. Other resources such as browser sessions and MCP servers retain their own lifecycles; this does not add a force-kill mechanism for Go tool goroutines.
- **The system must not** force-terminate goroutines via `runtime.Goexit`, `unsafe.Pointer` tricks, or process panic. The only honest mechanism in Go is cooperative `context.Cancel` + neutered output paths.
- **The system must not** restart the gateway process automatically in response to a stuck cancellation. Concurrent sessions must remain unaffected.
- **The system must not** offer a public `hard`/`--force` option or second-press-forces-immediately behavior. **Amended 2026-10-06:** a confirmed second Stop/Esc selects the tree-scope stop, with the same timing; `/stop` and `/stop-redirect` are separate commands, never aliases of `/cancel`.
- **The system must not** persist or replay the `{type: "turn_cancelled"}` JSONL entry as a chat-bubble message in the SPA. The entry is metadata for audit/replay tooling; the visible chat UI signal is the existing `(interrupted)` label derived from `{truncated: true}` on the partial assistant message.
- **The system must not** mutate `context.jsonl` (LLM history) on cancel — the partial assistant content stays as-is so the next turn's LLM sees natural truncation. Only `transcript.jsonl` receives the truncation flag and `turn_cancelled` entry.
- **The system must not** rate-limit `/cancel`. Q6 + Q11 already make cancel structurally idempotent and resource-cheap. Abuse-detection observability (FR-25a) replaces the missing rate-limit.
- **The system must not** treat `/cancel` as a session-end command. The session remains alive after cancel; the next user message starts a fresh turn in the same session.
- **The system must not** offer user-scoped authorization in v0.1 (only the turn initiator can cancel). Channel-scoped + identity attribution suffices for v0.1; user-scoped is deferred (tracked; no release scheduled).

---

## 7. Integration Boundaries

| External system | Data in | Data out | Failure behavior |
|---|---|---|---|
| **Telegram (BotCommands API)** — `telegram` factory | New `/cancel` definition via `RegisterCommands(ctx, defs)` | None (read-only registration) | Exponential backoff retry per `pkg/channels/telegram/command_registration.go:70-118`. Cancel still works via text parsing if registration fails. |
| **Slack** — `slack` factory | Slash command POST from Slack | 200 OK + ephemeral ack | Fallback to text parsing if interaction registration fails. |
| **Discord** — `discord` factory | Interaction event with command name `cancel` | Deferred response token | Same fallback. |
| **Teams / Feishu / DingTalk / Google Chat** — `teams`, `feishu`, `dingtalk`, `googlechat` | Adaptive card or platform-native command | Platform-specific ack | Each implements `RegisterCommands` following Telegram pattern; falls back to text parsing on registration failure. |
| **Matrix / IRC / LINE / WeChat / WeCom / QQ / OneBot / WhatsApp / WhatsApp Native** — `matrix`, `irc`, `line`, `weixin`, `wecom`, `qq`, `onebot`, `whatsapp`, `whatsapp_native` | Plain text message exactly matching `/cancel` | Plain text "✓ Cancelled by @user" reply | No registration needed; text-match in inbound message handler. |
| **OpenRouter LLM stream** (any provider) | Stream chunks until cancel | Context cancellation + explicit `providerCancel()` aborts in-flight HTTP request | Connection close is best-effort; partial chunk captured by `AddFullMessage` before cancel propagates. The LLM provider may receive a request-cancelled signal; if not honored server-side, we just stop reading and the connection drops. |
| **Audit log writer** (`pkg/audit/`) | `event_type: turn_cancel_attempt | turn_cancelled | turn_cancel_stuck` + metadata | One JSONL line per event | If audit write fails, falls back to structured log line at ERROR level; does not block cancel completion. The HMAC chain (shipped with #155) reads these entries via the existing audit emit path (FR-18 uses `audit.EmitEntry` — verified compatible). |
| **MCP server (in-flight RPC)** | Context cancellation propagates to RPC client | RPC client stops reading; server may continue computing | Document for operators: MCP server-side compute may continue past cancel. Mitigation: tool-author responsibility to make MCP tools cancel-aware. |

---

## 8. Existing Codebase Context (historical v2 implementation sketch)

**Amended 2026-10-06:** build against the one `AgentLoop.StopSession` path and durable-edge tree scope above, not the shared-transcript flow sketches below. Timer ownership is in the canonical Stop path, never a second gateway/agent timer family.

### Symbols involved

| Symbol | File:line | Role | Summary |
|---|---|---|---|
| `InterruptSession` | `pkg/agent/steering.go:382` | **MODIFIED** | Today targets first matching turn via `transcriptSessionID == sessionID`. Will cascade to all matching `turnState`s in parallel, calling `requestGracefulInterrupt` + `providerCancel` on each. Emits `turn_cancelled` audit event. |
| `InterruptGraceful` | `pkg/agent/steering.go:358` | **UNCHANGED (CALLED)** | Existing primitive — used internally by `InterruptSession` for each matched turnState. |
| `InterruptHard` (existing, `func() error`) | `pkg/agent/steering.go:412` | **UNCHANGED — NOT used by cancel** | Existing primitive operates on `getAnyActiveTurnState()`. NOT session-targeted. Cancel uses the new `InterruptSessionHard` instead. Existing callers (steering/test paths) preserved. |
| `InterruptSessionHard(sessionID, hint)` | `pkg/agent/steering.go` (NEW) | **NEW** | Walks `activeTurnStates`, calls `requestHardAbort()` + `providerCancel()` on every matching turnState. Used by the 3s timer escalation. |
| `requestGracefulInterrupt` | `pkg/agent/turn.go:336` | **CALLED** | Unchanged; called per-turnState during cascade. |
| `requestHardAbort` | `pkg/agent/turn.go` (existing) | **CALLED** | Unchanged; called per-turnState during hard cascade. |
| `turnState` struct | `pkg/agent/turn.go:49` | **MODIFIED** | New fields: `abandoned atomic.Bool`, `cancelMu sync.Mutex`, `cancelFired atomic.Bool`. Existing `providerCancel func()` field reused. |
| `transcriptSessionID` field | `pkg/agent/turn.go:125` | **READ** | Cascade key. Per FR-6a, sub-turns will newly inherit this from parent. |
| `processOptions.TranscriptSessionID` | `pkg/agent/loop.go` (existing field) | **WRITE-NEW** | Per FR-6a, `pkg/agent/subturn.go:435-451` MUST set this from parent's `transcriptSessionID` when building child processOptions. Currently not set → empty string for all sub-turns. |
| `activeTurnStates` | `pkg/agent/loop.go:75` | **READ** | `sync.Map` walked by cascade. Each `turnState` registered here at `runAgentLoop` start. |
| `handleCancel` | `pkg/gateway/websocket.go:734` | **MODIFIED** | Adds two-stage timer (3s graceful → hard via `InterruptSessionHard`, 3s post-hard → detach), records `turn_cancel_attempt` audit entry, calls cancel mutex protection. |
| `AppendTranscript` | `pkg/session/unified.go:294` | **CALLED** | Writes the `{type: "turn_cancelled"}` entry to `transcript.jsonl`. |
| `MarkLastEntryTruncated(sessionID)` | `pkg/session/unified.go` (NEW) | **NEW** | Reads the last `{role: "assistant"}` entry from `transcript.jsonl`; if its turn matches the cancelled turn, rewrites the entry with `truncated: true`. Acquires file lock per existing `fileutil.Flock` pattern. Does NOT touch `context.jsonl`. |
| `AddFullMessage` (context store) | `pkg/agent/loop.go:4418` | **UNCHANGED** | Continues to write partial content to `context.jsonl` (agent LLM history). Not mutated by cancel. |
| `EntryType` constants | `pkg/session/daypartition.go:31-37` | **EXTENDED** | New constant `EntryTypeTurnCancelled = "turn_cancelled"`. |
| `commands.Definition` | `pkg/commands/definition.go:23` | **EXTENDED** | New definition `/cancel` registered globally. |
| `commands.Runtime` | `pkg/commands/runtime.go:8` | **EXTENDED** | New method: `CancelActiveTurn(ctx, sessionID, canceller) error`. |
| `CommandRegistrarCapable` | `pkg/channels/interfaces.go:68` | **EXTENDED** | 6 new implementations: Slack, Discord, Teams, Feishu, DingTalk, Google Chat. |
| Web cancel transport | existing cancel frame | **Amended 2026-10-06 (founder)** | First Stop/Esc or `/stop` selects session scope and opens 3 s; second Stop/Esc within it or `/cancel` selects tree. Keep the existing transport; no new wire type. |
| Slash command catalog | chat command surface | **Amended 2026-10-06 (founder)** | `/stop`, `/cancel` and `/stop-redirect` remain distinct and available during streaming; `/cancel` means the whole helper tree, not just this turn. |
| `shouldShowSlash` | `src/components/chat/ChatScreen.tsx:456` | **MODIFIED** | Current: `inputValue.startsWith('/') && !isStreaming && !isReplaying && isConnected`. New: relax to allow streaming when at least one available command matches — filter `SLASH_COMMANDS` by `availableWhileStreaming` during stream. |
| Slash command dispatch | current chat | **Amended 2026-10-06 (founder)** | `/stop`: current turn plus 3 s; `/cancel`: immediate tree; `/stop-redirect <instruction>`: stop and continue this chat, root or helper. |
| Escape handler | current chat | **Amended 2026-10-06 (founder)** | First eligible Esc stops this turn and opens 3 s; a second inside the window selects the tree even after the first turn ended. Higher-priority dismissal consumes Esc without a Stop. |
| CLI input handling | interactive chat | **AMENDED 2026-10-06 (founder)** | Eligible Esc 1 / `/stop`: current chat turn plus 3 s window; Esc 2 within it: tree; `/cancel`: immediate tree. Preserve terminal key-sequence handling without the obsolete 500 ms double-Escape rule. |
| `audit.EmitEntry` | `pkg/audit/emit.go:34` | **CALLED** | Cancel handler emits via this helper. The HMAC chain (shipped with #155) reads from the same path. |
| MaixCam channel | `pkg/channels/maixcam/` | **DELETED** | Removed in prep commit per F-15. Also removes references in `manager.go`, `config.go`, `config_old.go`, REST endpoints, doctor command, OpenClaw migration. |

### Impact assessment

| Symbol | Risk | Direct (d=1) | Indirect (d=2) |
|---|---|---|---|
| `InterruptSession` (cascade) | **MEDIUM** | `handleCancel` (websocket.go:739), `commands.Runtime.CancelActiveTurn` (NEW) | `EventForwarder` emits `EventKindInterruptReceived`; agent loop dequeue |
| `InterruptSessionHard` (NEW) | **MEDIUM** | 3s-timer goroutine in cancel handler | Hard-abort cascade across all matching turnStates |
| `subturn.go` processOptions (transcriptSessionID inheritance) | **HIGH** (test fails on current main without this) | `runAgentLoop` for every child turn | All cascade behaviour |
| `turnState.abandoned/cancelMu/cancelFired` (NEW fields) | **LOW** | `runAgentLoop`, `Finish`, transcript-write helpers | Orphan watchdog, session rollback |
| `handleCancel` (two-stage timer + audit) | **CRITICAL** | WebSocket frame dispatcher (websocket.go:551) | Agent loop Steer queue, session meta Status |
| `MarkLastEntryTruncated` (NEW) | **MEDIUM** | Cancel handler (transcript-flag write) | UI replay; mu-coordinated with `AppendTranscript` |
| `AppendTranscript` (new entry type) | **LOW** | Cancel handler writes `turn_cancelled` entry | UI replay (must filter); session recovery |
| `shouldShowSlash` (modified) | **LOW** | Chat input rendering | Slash menu UX |
| MaixCam removal | **LOW** | `manager.go:468`, REST endpoints ×4, doctor command, OpenClaw migration (5 files) | Config migration warning path |

### Relevant execution flows

**Flow A — Web Stop controls**
First Stop/Esc or `/stop` → this chat's current turn, session scope, and 3 s window. Second Stop/Esc within it → this chat and its helper tree, tree scope. `/cancel` → tree scope immediately. All selected executions use the shared StopSession stages; no separate button or offer.

**Flow B — CLI and channel commands**
The same registered `/stop`, `/cancel` and `/stop-redirect <instruction>` meanings apply. Redirect stops and continues the chat where it was entered, root or helper; it does not choose a helper on the root's behalf.

**Flow C — tree-scope stop helper and background-shell scope**
A tree-scope stop follows durable helper edges, including queued helpers. Helpers keep their own transcripts. Plain Stop leaves helpers and background shells running; a second Stop / Esc within 3 s, or `/cancel` stops the tree and kills its background shells.

**Flow D — Partial assistant persistence (CORRECTED per F-04)**
Agent loop is streaming when cancel fires. Two stores receive different treatment:
1. `context.jsonl` (LLM history) — agent loop's `AddFullMessage` (loop.go:4418) writes the partial content. **NOT mutated by cancel.** The LLM next turn sees natural truncation.
2. `transcript.jsonl` (UI replay) — already received the partial assistant entry as it streamed (via `AppendTranscript`). Cancel handler calls **new** `MarkLastEntryTruncated(sessionID)` which acquires the file lock, reads the last `{role: "assistant"}` entry for this turn, rewrites it with `{truncated: true}`. Then `AppendTranscript` adds a separate `{type: "turn_cancelled"}` entry. Both are under the same lock.

**Flow E — Orphan watchdog + cancel interaction**
Orphan watchdog (60s, `websocket.go:1241`) is for synthesizing UI frames when a sub-turn span doesn't close after parent ends. With cascade in place (post-F-01 fix), parent and sub-turns end together — watchdog rarely fires. When it does (stuck detach case), it emits `subagent_end{status:"interrupted"}` as today; the cancel feature's `turnState.abandoned` independently suppresses any later frames from the actually-stuck goroutine.

**Flow F — CLI Stop sequence**
Eligible Esc 1 stops this chat's current turn and opens 3 s; Esc 2 within it stops this chat and its helper tree. A terminal arrow/function-key sequence is not a standalone Esc. `/stop`, `/cancel` and `/stop-redirect` have the same scope as on web and channels. No separate byte-timing or confirmation mechanism is specified here.

**Flow G — UI during 3s graceful window for stuck cancel (new, answers unasked Q3)**
At t=0 user clicks Stop. Web button morphs to "Stopping..." with spinner (immediate, <100ms local React state). At t=3s if cancel hasn't completed, button label morphs to "Force-stopping..." (server pushed event `cancel_stage: hard`). At t=6s if detach fires, button morphs to "Cancelled" (server pushed event `cancel_stage: detached`); chat input re-enabled for new message; partial assistant message shows `(interrupted)` suffix.

### Channel command-registration template (from Telegram)

`pkg/channels/telegram/command_registration.go:32-58` is the reference pattern. Each Tier A implementer copies the structure: convert `commands.Definition[]` → platform-specific shape; optional idempotency check (skip if registered set matches); call platform's set-commands API. Async retry with exponential backoff on transient failures, log + continue on permanent failures (FR-28 fallback to text parsing).

---

## 9. BDD Scenarios

**Amended 2026-10-06:** old scenarios saying a plain Stop stops helpers are superseded: use `/cancel` or a second Stop / Esc within 3 s for the cascade oracle, and add the self-only control where helpers keep working. Every timing assertion uses polite -> force at 3 s -> detach 3 s after force. Existing test names are historical/proposed coverage, not proof that those tests ran.

### Happy paths

#### Scenario: Sub-turn `transcriptSessionID` inheritance (gate test — fails on current main)
**Traces to:** FR-6a, US-4.1
**Given** `subturn.go` builds processOptions for a child turn with the parent's `transcriptSessionID == "S"`
**When** the child registers itself in `activeTurnStates`
**Then** `activeTurnStates.Range` matching `ts.transcriptSessionID == "S"` finds both parent and child entries.

#### Scenario: Web stop button cancels parent + all sub-turns within 5 seconds
**Traces to:** US-1.1, US-1.2, US-4.1
**Given** the SPA is mounted on a live session
**And** the agent has spawned 2 sub-turns currently executing
**When** the user clicks the Stop button
**Then** within 5 seconds the parent turn and both sub-turns exit
**And** the partial assistant message displays with the `(interrupted)` suffix (rendered from `transcript.jsonl` `{truncated: true}` flag)
**And** exactly one `event_type: turn_cancel_attempt{was_fired: true}` and one `event_type: turn_cancelled` audit entry exist (the latter with `descendants_cancelled.length == 2`)
**And** the chat input is re-enabled.

#### Scenario: Telegram user types /cancel during streaming response
**Traces to:** US-2.1, US-2.2
**Given** a Telegram chat with an active streaming turn
**When** the user selects `/cancel` from Telegram's native autocomplete
**Then** within 5 seconds the streaming stops
**And** `turnState.providerCancel()` was called for the OpenRouter HTTP request
**And** the bot's "⏸ Cancelling..." message is edited to "✓ Cancelled by @user"
**And** the audit log records `canceller_channel: "telegram"` and `canceller_user: "@user"`.

#### Scenario: IRC user sends /cancel as plain text
**Traces to:** US-2.3, US-2.4
**Given** an IRC channel with an active turn
**When** the user sends `/cancel` (case-insensitive, trimmed, whole-message)
**Then** within 5 seconds the bot stops generating
**And** the bot sends two messages: "⏸ Cancelling..." then "✓ Cancelled by @user".

#### Scenario: CLI first Esc stops this turn; second Esc stops the tree
**Traces to:** US-3, FR-3, FR-31a
**Given** this chat has an active turn, a helper and a background shell
**When** the user presses eligible Esc once
**Then** only this chat's current turn stops, its helper and background shell keep running, and a 3 s window opens
**When** the user presses eligible Esc again within that window
**Then** A tree-scope stop reaches this chat and its whole helper tree and kills their background shells
**And** no separate button is needed.

### Alternate paths

#### Scenario: Web slash menu /cancel works DURING streaming (per FR-3a)
**Traces to:** US-1.3
**Given** a turn is streaming AND the user types `/` into the chat input
**When** `shouldShowSlash` evaluates filters by `availableWhileStreaming`
**Then** the slash menu includes `/stop`, `/cancel` and `/stop-redirect` as commands available while streaming
**And** selecting `/cancel` immediately selects tree scope, unlike the first Stop press's session scope.

#### Scenario: Web Escape key during inference cancels
**Traces to:** US-1.4
**Given** the chat input has focus and a turn is streaming
**When** the user presses Escape
**Then** `cancelStream()` is called
**And** the turn ends within 5 seconds.

#### Scenario: Pending tool approval auto-denies on cancel
**Traces to:** US-4.2
**Given** session S has a pending exec approval modal, tool not yet started
**When** `/cancel` fires
**Then** the approval is auto-denied with `reason: "session cancelled"`
**And** the approval modal closes (web) or the approval prompt disappears (chat)
**And** the turn loop unblocks and exits within 3 seconds.

#### Scenario: Already-executing tool not killed by cancel
**Traces to:** US-4.4
**Given** session S has an exec tool whose goroutine has started and is mid-execution
**When** `/cancel` fires
**Then** the tool's context is cancelled (via its caller's context chain)
**And** the tool's goroutine is NOT killed forcibly
**And** the tool's eventual exit is governed by its own context-handling.

### Error paths

#### Scenario: Graceful does not complete; hard cancel escalates after 3s using new `InterruptSessionHard`
**Traces to:** FR-11
**Given** a tool call is in a blocking operation that ignores context for >3 seconds
**When** `/cancel` fires
**Then** at t=0 `InterruptGraceful` is requested via cascade + `providerCancel()` fires
**And** at t=3s `InterruptSessionHard(sessionID)` is invoked — walks `activeTurnStates` and calls `requestHardAbort()` on every matching turnState
**And** the audit entry's `cancel_method` is `"hard"`.

#### Scenario: Hard cancel does not cause goroutine exit; detach after 3s (amended 2026-10-06)
**Traces to:** US-6.1, US-6.4
**Given** the tool ignores both graceful and hard cancel for >6 seconds total
**When** 3 seconds elapse after hard cancel
**Then** `turnState.abandoned = true` is set atomically using `time.AfterFunc(3*time.Second, ...)` started after hard cancel (monotonic clock)
**And** any subsequent transcript-write attempt from that turn is suppressed
**And** the metric `omnipus_abandoned_writes_suppressed_total` increments on each suppressed write
**And** an audit entry `event_type: turn_cancel_stuck` is written with `goroutine_age_after_hard_cancel: ~3s`.

#### Scenario: Other sessions unaffected by stuck cancel
**Traces to:** US-6.2, US-6.3
**Given** session A is in the detached-and-neutered state
**When** session B sends a message
**Then** B's turn runs at normal speed
**And** B's transcript writes succeed, frames emit normally, cost accumulator advances normally.

#### Scenario: Second cancel during graceful window is a no-op (cancelFired protects)
**Traces to:** Q6, FR-13a
**Given** `/cancel` was issued at t=0 (took `cancelMu`, set `cancelFired=true`, released)
**When** a second `/cancel` is issued at t=1s
**Then** the second cancel handler sees `cancelFired==true` and exits
**And** records `turn_cancel_attempt{was_fired: false}` audit entry only
**And** does not write a duplicate `turn_cancelled` entry.

#### Scenario: Cancel of session with no active turn — records attempt, no fire
**Traces to:** EC-1, FR-20
**Given** session S has no active turn (idle)
**When** `/cancel` fires
**Then** `turn_cancel_attempt{was_fired: false}` audit entry is written
**And** no `turn_cancelled` entry is written
**And** no transcript entry is written
**And** in web, the Stop button is disabled; in chat channels, no reply is sent.

### Edge cases

#### Scenario: Cancel race with natural completion (cancelMu protects)
**Traces to:** EC-2, FR-13a
**Given** a turn is about to write its final assistant entry and call `Finish`
**When** `/cancel` fires within 50ms of natural completion
**Then** whichever path wins the `cancelMu` exclusively writes the terminal state
**And** if `Finish` won, cancel records `turn_cancel_attempt{was_fired: false}` and exits with no `turn_cancelled` entry, no truncation flag, no chat reply
**And** if cancel won, the `{truncated: true}` flag is set on the partial assistant entry and the `turn_cancelled` entry is written.

#### Scenario: Cancel during replay/attach is silent
**Traces to:** EC-3
**Given** a session is being replayed (no active turn in `activeTurnStates`)
**When** `/cancel` fires
**Then** the cascade finds zero matches
**And** `turn_cancel_attempt{was_fired: false}` audit entry is written
**And** replay continues unaffected.

#### Scenario: Cross-channel race — same session cancelled from web and Telegram simultaneously
**Traces to:** EC-4
**Given** session S has cancel arriving from web at t=0 and Telegram at t=0+5ms
**When** both calls reach `handleCancel`
**Then** one acquires `turnState.cancelMu` first and sets `cancelFired=true`; the other sees `cancelFired==true` and returns no-op
**And** exactly one `turn_cancelled` JSONL entry is written
**And** two `turn_cancel_attempt` audit entries are recorded (one `was_fired: true`, one `was_fired: false`).

#### Scenario: CLI multi-byte arrow-key sequence does not cancel
**Traces to:** EC-12, US-3.4
**Given** the CLI is in inference mode with raw-stdin polling
**When** the user presses the Up arrow (which sends 0x1B 0x5B 0x41)
**Then** the raw-stdin goroutine sees 0x1B, buffers it, sees 0x5B within 50ms — recognizes CSI start
**And** discards the buffered Escape (no cancel)
**And** passes the bytes through.

#### Scenario: Tier B parse table (outline)
**Traces to:** US-2.3, EC-7

```
Examples: Tier B text-parse acceptance
  | input                       | should-trigger |
  | "/cancel"                   | yes            |
  | "/CANCEL"                   | yes            |
  | "  /cancel  "               | yes            |
  | "/Cancel"                   | yes            |
  | "/cancel my reservation"    | no             |
  | "Hey /cancel"               | no             |
  | "//cancel"                  | no             |
  | ""                          | no             |
  | "cancel"                    | no             |
```

#### Scenario: Channel-side message edit fails (rate-limited)
**Traces to:** EC-10
**Given** a Tier A channel sent "⏸ Cancelling..." then the platform returns 429 on edit
**When** the edit fails
**Then** a new "✓ Cancelled by @user" message is sent instead
**And** the audit log records `channel_edit_failure: true` field.

#### Scenario: Stuck cancel UI progression
**Traces to:** EC-15
**Given** a cancel is fired and the loop is stuck
**When** t=0 → 3s → 6s elapse
**Then** the Stop button label morphs: t=0 "Stopping..." → t=3s "Force-stopping..." → t=6s "Cancelled"
**And** chat input is re-enabled at t=6s.

#### Scenario: Sub-turn provider cancel during cascade
**Traces to:** EC-14
**Given** parent and 2 sub-turns are all in active LLM streams
**When** `/cancel` fires
**Then** each turnState's `providerCancel()` is called concurrently
**And** all 3 OpenRouter HTTP requests are aborted within 200ms (not waiting for natural drain).

---

## 10. Test-Driven Development Plan (historical names, current scope/timing below)

**Amended 2026-10-06:** T0 shared-transcript inheritance is superseded by own helper transcripts and durable-edge tree-scope stop scope. T1/T3/T4 cascade tests target the tree-scope stop, with plain Stop as a helper-preserving control. Timer tests exercise the one StopSession path for both people and agents; no independent gateway or delegate timer is authorized.

### Test implementation order

| # | Test name | Level | Traces to | Description |
|---|---|---|---|---|
| **T0** | `TestSubTurnInheritsTranscriptSessionID` | Unit (Go) | FR-6a, F-01 | **GATE TEST — fails on current `main`.** Build a `processOptions` for a sub-turn from a parent with `transcriptSessionID="S"`; call `newTurnState`; assert child's `transcriptSessionID == "S"`. |
| T1 | `TestInterruptSession_CascadeToSubTurns` | Unit (Go) | US-4.1 | Register parent + 2 sub-turns in `activeTurnStates` with same `transcriptSessionID`; call `InterruptSession`; assert all 3 received `requestGracefulInterrupt` AND `providerCancel`. |
| T2 | `TestInterruptSession_NoActiveTurnIsAttemptOnly` | Unit (Go) | EC-1, FR-20 | Empty `activeTurnStates`; call `InterruptSession`; assert no `turn_cancelled` audit entry; assert one `turn_cancel_attempt{was_fired: false}` entry. |
| T3 | `TestInterruptSession_SecondCallDuringGracefulIsNoOp` | Unit (Go) | Q6, FR-13a | First cancel sets `cancelFired=true`; second cancel within window; assert second returns early; assert second emits `turn_cancel_attempt{was_fired: false}`; assert no duplicate `turn_cancelled`. |
| T4 | `TestInterruptSessionHard_CascadesAcrossSession` | Unit (Go) | FR-11, F-03 | Register parent + 2 sub-turns with same `transcriptSessionID`; call new `InterruptSessionHard(sid)`; assert all 3 received `requestHardAbort()` AND `providerCancel()`. Existing `InterruptHard()` test is separately preserved. |
| T5 | `TestTurnState_AbandonedSuppressesWrites` | Unit (Go) | US-6.1 | Set `turnState.abandoned = true`; call write helper; assert no file change; assert metric `omnipus_abandoned_writes_suppressed_total` incremented. |
| T6 | `TestCancelTimer_EscalatesGracefulToHardAt3s` | Unit (Go) | FR-11 | Mock-clock-driven test: start cancel; advance 3s; assert `InterruptSessionHard` was called with the correct sessionID; assert `cancel_method` in final audit is `"hard"`. |
| T7 | `TestCancelTimer_DetachesAt3sAfterHard` | Unit (Go) | FR-12, US-6.1 | Mock-clock-driven: advance 3s (hard) + 3s (detach); assert `abandoned=true` on every matching turnState; assert `turn_cancel_stuck` audit entry written; assert metric incremented. |
| T8 | `TestAuditLog_ThreeEventTypes` | Unit (Go) | US-5.1, US-5.2 | Trigger three scenarios (no active turn / cancel fired / cancel + detach); assert audit log has correct event types per case (`turn_cancel_attempt`, `turn_cancelled`, `turn_cancel_stuck`). |
| T9 | `TestTranscript_TruncatedFlagOnPartialAssistant_TranscriptOnly` | Unit (Go) | FR-14, F-04 | Stream partial content (writes to both transcript and context stores); fire cancel; assert `transcript.jsonl` last assistant entry has `truncated: true`; assert `context.jsonl` entry is **unchanged** (no truncated flag). |
| T10 | `TestReplayFilter_TurnCancelledEntryNotInChatBubble` | Unit (TS, vitest) | FR-16 | Replay path consumes JSONL with `{type: "turn_cancelled"}` entry; assert no chat bubble rendered for it; assert preceding `{role: "assistant", truncated: true}` entry IS rendered with `(interrupted)` suffix. |
| T11 | `TestTierBParse_TableDriven` | Unit (Go) | EC-7, FR-2 | 9-row table from §9 scenario outline; assert correct trigger behavior per row. |
| T12a | `TestRegisterCommands_Telegram_ExactCommandSet` | Integration (Go) | US-2.1, F-20 | Existing Telegram test extended: register `/stop`, `/cancel` and `/stop-redirect`; assert the exact registered set includes all three plus the existing commands, with no aliases. |
| T12b–g | `TestRegisterCommands_{Slack,Discord,Teams,Feishu,DingTalk,GoogleChat}` | Integration (Go) | US-2.1 | NEW scaffold per channel: register `/cancel` against mocked platform client; assert platform's command-set API received `/cancel`. |
| T13 | Stop and slash-command scope | Unit (TS, vitest) | US-1.3 | First Stop or `/stop` selects session scope and opens 3 s; second Stop/Esc inside it and `/cancel` select tree. No separate button or offer. |
| T14 | Eligible Escape scope | Unit (TS, vitest) | US-1.4 | Esc consumed by a menu/dialog/editor is dismissal only. First eligible Esc stops this turn and opens 3 s; second within it selects tree, including after the first turn ended. After expiry it is a first press again. |
| T15 | Commands available during streaming | Unit (TS, vitest) | FR-3a | `/stop`, `/cancel` and `/stop-redirect` are reachable during streaming and retain their distinct meanings. |
| T16 | CLI Stop sequence | Integration | US-3 | Exercise one eligible Esc, a second within 3 s, a second after expiry, `/cancel`, and `/stop-redirect` in root and helper chats. Assert chat/tree scope and background-shell behavior, not only an interrupted label. |
| T17 | `TestCli_ArrowKeyDoesNotCancel` | Unit (Go) | EC-12, US-3 | Drive raw-stdin handler with 0x1B 0x5B 0x41 (Up arrow); assert cancel NOT called. |
| T17b | Single CLI Escape stops only this turn | Unit (Go) | US-3 | Esc 1 stops the current chat turn and opens 3 s; helpers and background shells keep running. Replace the old “single Esc does not cancel” oracle. |
| T18 | `TestSessionIsolation_StuckCancelDoesNotAffectSiblings` | Integration (Go) | US-6.3 | Create session A with fake-blocking-tool; cancel A; concurrently send message to session B; assert B completes normally while A is in detach state. |
| T19 | `TestPendingApproval_AutoDeniedOnCancel` | Integration (Go) | US-4.2 | Create turn with `ask` policy exec call; before user clicks Allow/Deny, fire cancel; assert approval resolves to `denied` with `reason: "session cancelled"`. |
| T20a | `TestAbuseDetection_HighCancelRateEmitsWarning` | Integration (Go) | FR-25a, F-08 | Issue 10 cancel attempts from same user in 60s; assert audit log has WARNING entry `event_type: cancel_abuse_pattern` with canceller identity. |
| T20b | `TestProviderCancel_FiresOnGraceful` | Integration (Go) | FR-12a, EC-13 | Start a turn with a slow LLM stream; fire cancel; assert `providerCancel()` was called within 100ms of cancel-issued (verified by ProviderCancel mock spy); assert HTTP request was cancelled within 200ms. |
| T21 | `cancel-cross-channel.spec.ts :: web stop button` | E2E (Playwright) | US-1.1, SC-1 | Open SPA; send prompt → long output; click Stop; assert within 5s message shows `(interrupted)` and input enabled. |
| T22 | `cancel-cross-channel.spec.ts :: web slash menu during streaming` | E2E (Playwright) | US-1.3, FR-3a | Same setup; mid-stream, type `/c`; assert `/cancel` shows in menu; select it; assert cancel behavior. |
| T23 | `cancel-cross-channel.spec.ts :: web Escape key` | E2E (Playwright) | US-1.4 | Same setup; press Escape; assert cancel. |
| T24 | cascade stopping the chat and its helper tree | E2E | US-4.1 | First Stop leaves the helper and background shell running; second Stop/Esc within 3 s or `/cancel` stops the tree and kills its background shells. |
| T25 | `cancel-cross-channel.spec.ts :: stuck cancel UI progression` | E2E (Playwright) | EC-15 | Trigger cancel of a stuck operation; observe button text morph through "Stopping..." → "Force-stopping..." → "Cancelled". |
| T26 | `cancel-cross-channel.spec.ts :: audit entries exist` | E2E (Playwright + fs check) | US-5.1, US-5.2 | Trigger cancel; read audit JSONL via Node fs; assert `turn_cancel_attempt{was_fired: true}` and `turn_cancelled` entries exist. |
| T27 | `TestMaixCamRemoval_GrepClean` | Integration (shell-via-Go) | US-7.1 | After prep commit: `grep -r "maixcam" --include="*.go" --include="*.ts" .`; assert exit code 1 (no matches). |
| T28 | `TestConfigMigration_LegacyMaixCamGracefullyIgnored` | Unit (Go) | US-7.2, US-7.3 | Load a config.json with `maixcam` section; assert no panic; assert warning logged; assert next save strips the section. |

### Test datasets

#### Dataset T1-D1: `activeTurnStates` cascade matrix
| Scenario | Turns registered (with transcriptSessionID) | Expected cancels |
|---|---|---|
| Parent only | parent (sid=S) | 1 |
| Parent + 1 sub (post FR-6a fix) | parent (sid=S), sub1 (sid=S) | 2 |
| Parent + 3 subs | parent (sid=S), sub1-3 (sid=S) | 4 |
| Parent + sub in other session | parent (sid=S), sub (sid=OTHER) | 1 (only parent) |
| Empty | none | 0 |
| Sub with empty transcriptSessionID (pre-fix) | parent (sid=S), sub (sid="") | 1 (only parent; this is the bug case used to verify F-01 fix) |

#### Dataset T11-D1: Tier B text-parse boundaries (9 rows)

See §9 scenario outline; same table.

#### Dataset T6-D1 / T7-D1: stuck-loop timing boundaries
| t (s after cancel-issued, monotonic) | Expected state | Audit emit |
|---|---|---|
| 0 | graceful initiated; providerCancel called | (start of `turn_cancelled` write — but written at completion) |
| 1.5 | graceful in progress | (none) |
| 3 | hard escalation via `InterruptSessionHard` | (still single `turn_cancelled` at completion with `cancel_method: "hard"`) |
| 6 | hard in progress, awaiting goroutine exit | (none) |
| 8 | detach fired (`abandoned=true`) via `time.AfterFunc` | `turn_cancel_stuck` WARNING |
| 60 | gateway continues; abandoned goroutine still alive (if so); metric counts each suppressed write | (none periodic) |

### Regression test requirements

Existing tests that MUST continue passing unchanged:
- `pkg/agent/steering_test.go` — `TestInterruptGraceful`, `TestInterruptHard` (existing `InterruptHard()` primitive is untouched; only NEW `InterruptSessionHard` joins it).
- `pkg/gateway/websocket_test.go` — existing `handleCancel` tests; new tests extend coverage.
- `pkg/channels/telegram/command_registration_test.go` — adding `/cancel` to registered set must not break existing tests.
- `tests/e2e/chat.spec.ts` — chat-send-and-receive baseline.
- `tests/e2e/replay-fidelity.spec.ts` — replay handling of new entry type (filtered).

New regression assertion (per F-20):
- `T12a` asserts EXACT command-set equality — future alias additions (e.g., `/stop`) break the test loudly.

---

## 11. Functional Requirements

**Amended 2026-10-06 — founder decision:** the effective replacements below govern every old scenario/test row that cites these FRs. Unrelated channel/audit behavior is not re-decided here.

| Requirement | Effective requirement replacing older scope/mechanism |
|---|---|
| FR-1 / FR-3 / FR-3a / FR-4 / FR-5 | First Stop/Esc or `/stop`: this chat's turn only plus 3 s window. Second Stop/Esc within it: chat plus whole helper tree. `/cancel`: immediate tree. No separate button. `/stop-redirect`: stop and continue this chat, root or helper, on web, CLI and channels. |
| FR-6 / FR-6a | the tree-scope stop walks durable helper edges; own transcript identities are not inherited to create a cascade. Plain Stop must never reach helpers. |
| FR-10 / FR-11 / FR-12 / FR-12a | All people/agent triggers enter `AgentLoop.StopSession`; polite at once, force at 3 s, detach 3 s after force. No public hard option, agent grace or cancel_grace. |
| FR-13 | Repeating the same scoped request is idempotent; confirming the tree-scope stop after a session-only Stop expands scope, not timing. |
| FR-21 | Report current stop stage truthfully using 3 s / subsequent 3 s timing; “Stopped” only after the owning execution's shutdown/settlement, never just because a request was accepted. |


### Cancel signal delivery

- **FR-1 — Amended 2026-10-06 (founder):** Tier A channels MUST register `/stop`, `/cancel` and `/stop-redirect <instruction>` with the same semantics as web and CLI, in root and helper chats.
- **FR-2 — Amended 2026-10-06 (founder):** Tier B channels MUST dispatch the same three commands from text messages. `/stop` and `/cancel` match whole commands, not fragments of prose; `/stop-redirect` takes the instruction in this chat. This changes no command into an alias.
- **FR-3 — Amended 2026-10-06 (founder):** Stop click 1 / eligible Esc 1 / `/stop` MUST stop this chat's current turn only and open a 3 s window. A second Stop / eligible Esc / `/stop` within it MUST stop this chat and its whole helper tree. `/cancel` MUST select that tree immediately. The second press IS the confirmation; there MUST NOT be a separate button or armed offer. Esc consumed by a menu/dialog/editor MUST dismiss only that surface.
- **FR-3a — Amended 2026-10-06 (founder):** the SPA MUST keep `/stop`, `/cancel` and `/stop-redirect` reachable during streaming, with the same distinct scopes as on CLI and channels.
- **FR-4**: System MUST acknowledge a cancel request with a visible UI signal. Latency budget:
  - Web/CLI: ≤100ms (local React state morph / terminal redraw)
  - Chat channels: ≤500ms P95 (platform API round-trip)
- **FR-5 — Amended 2026-10-06 (founder):** `/stop`, `/cancel` and `/stop-redirect` are distinct commands, not aliases. `/stop-redirect <instruction>` MUST stop this chat's current turn and continue THIS chat with the instruction, in ANY root or helper chat. No root refusal and no implicit helper selection. Blank instruction returns usage without a stop; control/storage failures remain visible.

### Cascade semantics

- **FR-6 — Amended 2026-10-06:** StopSession with session scope MUST stop only that current turn and MUST NOT reach helpers. Tree scope (`/cancel` or a second Stop / Esc within 3 s) MUST walk the selected session's whole durable helper tree, including queued helpers; never ancestors/siblings. Report acceptance/pending/landed/incomplete honestly.
- **FR-6a — Amended 2026-10-06:** helpers MUST retain their own transcript identity/store. A tree-scope stop follows durable edges; shared transcript identity MUST NOT be restored as a cascade prerequisite. This replaces the old inherited-transcript requirement.
- **FR-7**: System MUST auto-deny pending tool approvals (tools that have NOT yet started execution) on the cancelled session and any sub-turn, with reason "session cancelled".
- **FR-8 — Amended 2026-10-06 (founder):** plain Stop MUST leave background shells running. a second Stop / Esc within 3 s, or `/cancel` MUST kill background shells belonging to the selected chat and its helper tree, leaving unrelated chats alone. Other already-executing Go tool goroutines retain their context-handling obligations; no force-kill of goroutines is introduced.
- **FR-9 — Amended 2026-10-06:** plain Stop MUST NOT stop any other session, including helpers. `/cancel` or a second Stop / Esc within 3 s may stop exactly the selected session's descendants, never ancestors, siblings or unrelated sessions.

### Two-stage timing

- **FR-10 — Amended 2026-10-06:** every person/agent trigger MUST enter the one StopSession path, with polite stop immediately for each execution selected by session/tree scope; no separate delegate or gateway timer family.
- **FR-11 — Amended 2026-10-06:** the shared Stop path MUST force any still-running selected execution 3 s after polite stop. Its delayed effects MUST NOT target a replacement. Internal hard-abort is a stage, never a caller-selectable `hard` option.
- **FR-12**: System MUST mark `turnState.abandoned = true` on any matching turnState still alive 3 seconds after hard cancel was fired, using `time.AfterFunc(3*time.Second, …)` based on monotonic clock (`time.Since`).
- **FR-12a**: System MUST call `turnState.providerCancel()` immediately on graceful cancel (not waiting for hard), so the in-flight LLM HTTP request is aborted within the 3s window rather than waiting for natural drain.
- **FR-13**: System MUST treat a second `/cancel` issued during the graceful window (0–3s) as a no-op at the cancel handler level (no duplicate `turn_cancelled` write).
- **FR-13a**: System MUST protect the cancel-vs-Finish race with `turnState.cancelMu sync.Mutex` and `turnState.cancelFired atomic.Bool`. Both the cancel handler and `Finish` MUST acquire the mutex before writing terminal state. Cancel handler MUST check `cancelFired` after acquiring; if already set, exit (no-op).

### Persistence

- **FR-14**: System MUST mutate the LAST `{role: "assistant"}` entry of the cancelled turn in `transcript.jsonl` to set `truncated: true`, via NEW `MarkLastEntryTruncated(sessionID)` in `pkg/session/unified.go`. The mutation acquires the same file lock as `AppendTranscript`.
- **FR-14a**: System MUST NOT mutate `context.jsonl` (LLM history) on cancel. The partial content remains as `AddFullMessage` wrote it; the next turn's LLM context naturally includes the truncated content.
- **FR-15**: System MUST write exactly one `{type: "turn_cancelled", session_id, turn_id, cancelled_by_user, cancelled_by_channel, cancel_method, at, descendants_cancelled}` JSONL entry to `transcript.jsonl` per fired cancel (i.e., when `cancelFired==true` was set). The new constant `EntryTypeTurnCancelled` is added to `pkg/session/daypartition.go`.
- **FR-16**: System MUST NOT render `turn_cancelled` JSONL entries as chat bubbles in the SPA — they are metadata. The visible chat UI signal is the `(interrupted)` suffix derived from the `truncated: true` flag on the preceding assistant entry.
- **FR-17**: System MUST suppress all transcript writes, WebSocket frame emissions, and cost-accumulator updates from a turnState whose `abandoned` flag is set. Every suppressed write MUST increment `omnipus_abandoned_writes_suppressed_total` metric.

### Audit

- **FR-18**: System MUST emit one `event_type: turn_cancel_attempt` audit log entry per cancel request received, REGARDLESS of whether it fired. Required fields: session_id, canceller_user, canceller_channel, was_fired, at. (This entry IS written even for no-active-turn cases; the BDD scenario in §9 reflects this.)
- **FR-19**: System MUST emit one `event_type: turn_cancelled` audit entry per fired cancel (when `cancelFired==true` was set). Required fields: session_id, turn_id, cancelled_by_user, cancelled_by_channel, cancel_method, descendants_cancelled, at.
- **FR-20**: System MUST emit one `event_type: turn_cancel_stuck` WARNING audit entry per fired detach. Required fields: session_id, turn_id, goroutine_age_after_hard_cancel, at.

### UI feedback

- **FR-21 — Amended 2026-10-06:** report the selected turn's stop stage truthfully: polite immediately, force at 3 s, detach 3 s after force if still running. Display stopped only after its owning execution has shut down and settled; a denied save or pending control is a visible error/pending state, not successful Stop. A plain Stop reply must never claim helpers stopped.
- **FR-22**: Tier A channels with `MessageEditor` capability MUST send a "⏸ Cancelling..." message immediately on cancel-receipt, then edit to "✓ Cancelled by @user" on completion.
- **FR-23**: Tier B channels without `MessageEditor` capability MUST send two messages: "⏸ Cancelling..." then "✓ Cancelled by @user".
- **FR-24 — Amended 2026-10-06 (founder):** after a stopped CLI turn settles, show its partial output as interrupted and return to the input prompt. One eligible Esc already stops this turn; do not wait for a second Esc to do so.
- **FR-25**: The cancellation message in chat channels MUST include the canceller's channel-side identity (display name preferred; fall back to unique ID).
- **FR-25a**: System MUST emit a WARNING audit entry `event_type: cancel_abuse_pattern` when a single canceller exceeds N=10 cancel attempts within M=60s for the same or different sessions on the same channel. This is observability, not rate limiting — the cancels still fire. Operators consume this signal to investigate. Threshold and window are hard-coded for v0.1; tunable later (no release scheduled).
- **FR-26 — Amended 2026-10-06 (founder):** the existing Stop MUST remain usable for the second press throughout the 3 s window, even if the first press has already ended the turn. Outside that window, ordinary no-active-turn behavior applies. No extra button is added.

### Channels & registration

- **FR-27**: Tier A channels (7 channels) MUST implement `CommandRegistrarCapable.RegisterCommands` following the Telegram reference pattern.
- **FR-28**: Tier A channel registration failures (platform API errors) MUST log a structured warning and fall back to text-parsing the inbound message — `/cancel` MUST work even if registration failed.
- **FR-29**: Tier B channels MUST NOT implement `CommandRegistrarCapable`; their inbound message handler MUST text-match `/cancel` per FR-2.

### Stuck-loop handling

- **FR-30**: System MUST handle a stuck goroutine by detaching its output paths, not by terminating the goroutine. Process-level escalation (panic, restart) is forbidden.
- **FR-31**: System MUST maintain full functionality of all other sessions when one session is in the detached state. Verifiable via SC-5.
- **FR-31a — Amended 2026-10-06 (founder):** CLI MUST use the same 3 s Stop/Esc interaction window: first eligible Esc stops this chat's turn, second within it stops the tree, after expiry the next is a first press. Terminal arrow/function-key sequences MUST NOT count as standalone Esc presses. Delete the obsolete 500 ms double-Escape mode; do not discard a standalone first Esc.

### MaixCam removal (prep)

- **FR-32**: System MUST remove `pkg/channels/maixcam/` (init.go + maixcam.go) in a prep commit before the cancel feature commits land.
- **FR-33**: System MUST remove all references to MaixCam from: `pkg/channels/manager.go:468`, `pkg/config/config.go:778`, `pkg/config/config_old.go:104,237,245`, `pkg/gateway/rest.go` (4 locations), `cmd/omnipus/internal/doctor/command.go:119`, `pkg/migrate/sources/openclaw/` (5 files).
- **FR-34**: System MUST handle a legacy config.json containing a `maixcam` section gracefully — log a structured warning, ignore the section, continue loading other channels. The next save MUST strip the section from disk (no downgrade compatibility for v0.1, which has not shipped publicly).

### Provenance & implementation guarantees (cross-cutting)

- **FR-35**: All audit emissions for cancel events MUST go through `audit.EmitEntry` (`pkg/audit/emit.go:34`). This guarantees HMAC-chain compatibility (shipped with #155) — chain-integrity came with it, without spec changes.
- **FR-36**: Cancel handler MUST NOT block on I/O for >100ms before initiating cascade. Audit log writes and channel-side message sends MUST happen on goroutines spawned from the handler.

---

## 12. Success Criteria

- **SC-1**: After Stop button is clicked in the web SPA on a session with parent + 2 sub-turns, all 3 turns exit and chat input becomes enabled within 5 seconds P95, 8 seconds P99 — measured via E2E test T21+T24 across 5 consecutive runs.
- **SC-2**: Across the 16 chat channels (Tier A + Tier B, MaixCam removed), every channel has its active turn cancelled by `/cancel` — measured as: integration tests T12a–g pass for Tier A; T11 passes for Tier B parse semantics.
- **SC-3**: After cancel fires, `transcript.jsonl` contains exactly one `{type: "turn_cancelled"}` entry per fired cancel; `context.jsonl` is unchanged (no `truncated` flag mutation).
- **SC-4**: Audit log contains exactly one `turn_cancel_attempt` per cancel request received and one `turn_cancelled` per fired cancel — verifiable via grep.
- **SC-5**: Session-isolation under stuck-tool conditions: P95 latency of session B's writes during session A's detached period equals baseline ±50ms. Measured via T18 in a Go benchmark harness running 100 ops on B while A is detached.
- **SC-6**: Orphan-loop count (goroutines whose `turnState.Finish` was not called within 8s of `/cancel`) drops from current N (measured pre-feature in Playwright suite traces) to ≤2% of cancels (the detached-and-neutered tail).
- **SC-7**: Playwright e2e suite reliability for the specific CI job `ci.playwright.e2e` (configured in `.github/workflows/pr.yml`): 5 consecutive runs achieve ≥ 90% pass rate. Reported separately from the orphan-loop count metric (per F-19).
- **SC-8**: After MaixCam prep commit: `grep -rn "maixcam\|MaixCam" --include="*.go" --include="*.ts" --include="*.md" .` returns zero matches in the repo (excluding the cancel spec/review files).
- **SC-9**: Cancel feature does not regress any existing Playwright e2e test (full suite pass rate ≥ current baseline).
- **SC-10**: Slash-menu shows `/cancel` during streaming and the user can complete an end-to-end cancel via the slash menu within 5 seconds — verified by T22.

---

## 13. Traceability Matrix

| FR | User Story | BDD Scenario(s) | Test |
|---|---|---|---|
| FR-1 | US-2 | Telegram-/cancel; Tier-A-autocomplete | T12a, T12b–g |
| FR-2 | US-2 | IRC-text-/cancel; Tier-B-outline | T11 |
| FR-3 | US-1, US-3 | Web/CLI first-Esc; second-press-within-3-s; expiry; slash commands | T13, T14, T15, T16, T21–T24 |
| FR-3a | US-1.3 | Slash-menu-during-streaming | T15, T22 |
| FR-4 | US-1, US-2 | Immediate-ack scenarios | T13, T21 |
| FR-5 | (negative) | EXACT command set | T12a (exact-set assertion) |
| FR-6 | US-4.1 | Cascade-3-turns | T1, T24 |
| FR-6a | (gate) | Sub-turn-transcriptSessionID-inherit | **T0** |
| FR-7 | US-4.2 | Pending-approval-auto-deny | T19 |
| FR-8 | (negative; US-4.4) | Tool-already-executing-not-killed | (manual verification + tool-context contract) |
| FR-9 | US-6.3 | Other-sessions-unaffected | T1 (other-session row), T18 |
| FR-10 | US-1 | Web-stop-button-100ms | T6, T21 |
| FR-11 | (timer) | Graceful-to-hard | T6 |
| FR-12 | US-6.1 | Hard-to-detach-5s | T7 |
| FR-12a | EC-13 | ProviderCancel-on-graceful | T20b |
| FR-13 | Q6 | Second-cancel-noop | T3 |
| FR-13a | EC-2, EC-4 | Cancel-race-mutex; Cross-channel-race | T3 (mutex), T18 (cross-session) |
| FR-14 | US-1, US-5 | Web-stop-truncated; Replay-shows-interrupted | T9, T10 |
| FR-14a | F-04 | Context-store-unchanged | T9 (asserts both stores) |
| FR-15 | US-5.2 | Audit-turn_cancelled-entry | T8, T26 |
| FR-16 | Q8 | Replay-filters-turn-cancelled | T10 |
| FR-17 | US-6.1 | Stuck-detach-suppresses-writes | T5 |
| FR-18 | US-5.1 | Attempt-audit-on-no-fire; Attempt-audit-on-fire | T8, T2 |
| FR-19 | US-5.2 | Cancelled-audit-on-fire | T8 |
| FR-20 | US-5.5 | Stuck-detach-audit | T7 |
| FR-21 | US-1 | Stuck-cancel-UI-progression | T25 |
| FR-22 | US-2.2 | Telegram-edit-in-place | T12b–g (Slack/Discord variants) |
| FR-23 | US-2.4 | IRC-two-message-fallback | T11 (Tier B variants) |
| FR-24 | US-3.3 | CLI-prompt-redraw | T16 |
| FR-25 | Q5 | Cancelled-by-@user identity | T12b–g, T20a |
| FR-25a | F-08 | Abuse-detection-warning | T20a |
| FR-26 | EC-1 | Button-disabled-no-active-turn | T2 |
| FR-27 | US-2.1 | Tier-A-autocomplete | T12a, T12b–g |
| FR-28 | (alt) | Registration-fallback-text-parse | T12b–g fallback variants |
| FR-29 | US-2.3 | IRC-text-/cancel | T11 |
| FR-30 | US-6 | Stuck-detach | T5, T7 |
| FR-31 | US-6.3 | Other-sessions-unaffected | T18 |
| FR-31a | US-3.4 | Arrow-key-no-cancel; Single-Esc-no-cancel | T17, T17b |
| FR-32 | US-7.1 | MaixCam-grep-clean | T27 |
| FR-33 | US-7.1 | MaixCam-grep-clean | T27 |
| FR-34 | US-7.2, US-7.3 | Legacy-config-graceful; Strip-on-save | T28 |
| FR-35 | (cross) | (verified by FR-18/19/20 emit path = audit.EmitEntry) | T8 |
| FR-36 | (cross) | Audit-write-non-blocking | (covered by ≤100ms FR-4 + audit-async wiring) |

Every FR in traceability. Every BDD scenario traces to ≥1 FR.

---

## 14. Ambiguity Warnings

| # | Ambiguity | Resolution / Status |
|---|---|---|
| AMB-1 | Graceful 3s deadline tunable? | **RESOLVED** — hard-coded (Q3). |
| AMB-2 | UI sequencing for sub-turn cancellation? | **RESOLVED** — reuse `subagent_end{status:"interrupted"}` plumbing for SubagentBlock; no new UI primitives. |
| AMB-3 | Cascade walk parallel or sequential? | **RESOLVED** — parallel; per-match goroutine spawned in `activeTurnStates.Range`. |
| AMB-4 | Cancel ack message via agent persona or system? | **RESOLVED** — system message; templated text; no LLM round-trip. |
| AMB-5 | Telegram tests verify all commands preserved? | **RESOLVED** — T12a asserts EXACT command set (per F-20). |
| AMB-6 | Canceller identity uses display name or unique ID? | **RESOLVED** — display name preferred; unique ID fallback. |
| AMB-7 | LLM partial-chunk malformed handling? | **RESOLVED** — validate before appending; on validation failure, write entry with `{content: "", truncated: true, parse_error: true}`. |
| AMB-8 | CLI raw-stdin coordination with readline? | **RESOLVED** — `stdinOwner atomic.Pointer[string]` protocol; readline regains stdin after inference goroutine exits. |
| AMB-9 | Gateway restart during cancel? | **ACCEPTED** — cancel state in-memory only; on restart turnState is gone; audit log records the partial cancel attempt. |
| AMB-10 | Audit log retention / GDPR for canceller user IDs? | **ACCEPTED** — same retention as existing audit log policy; no change. |
| AMB-11 | Tier A platform registration blocked by policy? | **RESOLVED** — text-parse fallback per FR-28. |
| AMB-12 | Platform-side rate limits on `/cancel`? | **ACCEPTED** — platform handles it; spec doesn't intervene. |
| AMB-13 | Lock used for `turn_cancelled` write? | **RESOLVED** — same `fileutil.Flock` as `AppendTranscript` + new `cancelMu` for cancel-vs-Finish race. |
| AMB-14 | Terminal emulator Escape capture | **Amended 2026-10-06 (founder):** eligible standalone Esc follows the shared first/second 3 s rule; terminal key sequences are not Stop presses. The old 500 ms mode is withdrawn, not an additional option. |
| AMB-15 | Audit timestamp precision? | **RESOLVED** — UTC, RFC3339Nano. |
| AMB-16 (NEW) | Provider-cancel path on sub-turns? | **RESOLVED** — each sub-turn has its own `providerCancel`; cascade calls all (FR-12a). |
| AMB-17 (NEW) | Cost-accounting boundary at abandonment? | **RESOLVED** — costs incurred before abandonment count normally; only writes/frames/cost-ticks AFTER abandonment are suppressed (per FR-17). Operators see normal billing for in-flight tokens and zero for post-abandonment. |
| AMB-18 (NEW) | MCP in-flight RPC during cancel? | **RESOLVED + DOCUMENTED LIMITATION** — context cancel propagates to RPC client (standard wiring); MCP server may continue computing on its side; operators of expensive MCP servers should make tools cancel-aware. |
| AMB-19 (NEW) | Race winner of cancel vs natural-completion in chat channels? | **RESOLVED** — if natural-completion wins (`cancelFired==false` when cancel handler enters), NO "Cancelled by @user" message is sent; the bot's natural reply stands. EC-2 reflects this. |
| AMB-20 (NEW) | MaixCam config strip on save? | **RESOLVED** — next save strips the section (FR-34). |
| AMB-21 (NEW) | Abuse-detection threshold tunable? | **DEFERRED** — N=10 / M=60s hard-coded in v0.1; tunable later (no release scheduled) if operator feedback warrants. |

---

## 15. Holdout Evaluation Scenarios

(NOT in traceability matrix; for post-implementation external verification only.)

- **H-1**: A user with no prior knowledge of the cancel feature figures out how to stop an in-flight turn within 30 seconds of seeing it run amok, on both web (Stop button) and a chat channel (`/cancel`).
- **H-2**: In a Telegram group with two users, when User A is interacting with a bot that's stuck, User B can `/cancel` and the bot's confirmation message clearly identifies User B as canceller.
- **H-3**: A user with multiple browser tabs open on different sessions cancels one tab's session without affecting the others — verified by manual inspection.
- **H-4**: When a tool gets stuck, the user sees "Cancelled" within 10 seconds and the chat input is enabled for new messages.
- **H-5**: When the gateway is restarted while a cancel is mid-flight, the next gateway boot has no stale "cancelling" state.
- **H-6**: A user typing `/cancel my dinner reservation` in any Tier B channel does NOT trigger cancel — the bot responds normally.
- **H-7**: After cancel, an immediate follow-up message has the LLM aware of the partial cancelled content (because `context.jsonl` was not mutated) and responds coherently.

---

## 16. Implementation phasing within v0.1

Per F-09 review re-baseline: separated "core cancel mechanics" (deliverable at v0.1 release) from "Tier A native registration polish" (incremental, can land in v0.1 patch releases or be pushed if time-constrained).

### Phase 0 — Prep (1-2h, separable)

1. **Prep commit (standalone, separately revertable):** Remove MaixCam channel entirely (FR-32, FR-33, FR-34). Includes migration test (T28). Per F-15, this commit lands FIRST and stands alone — if cancel feature is later reverted, this stays.

### Phase 1 — Core cancel mechanics (3-5 days)

**These FRs cover the load-bearing release-blocking bug fix.** Without these, v0.1 cannot ship.

2. **F-01 gate fix:** `subturn.go` inherits `TranscriptSessionID` from parent processOptions. Test T0 (which fails on current `main`). FR-6a.
3. **Backend cascade:** Modify `InterruptSession` to walk `activeTurnStates` and cascade. Add NEW `InterruptSessionHard`. FR-6, FR-11. Tests T1, T4.
4. **Backend turnState fields:** Add `abandoned`, `cancelMu`, `cancelFired` to `turnState`. FR-12, FR-13a. Tests T3, T5.
5. **Backend two-stage timer + provider cancel + audit emit:** `handleCancel` adds 3s graceful → hard timer using monotonic clock; `providerCancel()` called on graceful; emits `turn_cancel_attempt` + `turn_cancelled` + `turn_cancel_stuck`. FR-10–FR-12a, FR-18–FR-20. Tests T2, T6, T7, T8, T20b.
6. **Backend pending-approval auto-deny:** wire cancel into approval-pending resolver. FR-7. Test T19.
7. **Backend transcript store:** `MarkLastEntryTruncated` (new); `turn_cancelled` entry via `AppendTranscript`. FR-14, FR-14a, FR-15. Test T9.
8. **Backend abuse-detection metric:** track per-canceller rate; emit `cancel_abuse_pattern` warning. FR-25a. Test T20a.
9. **Frontend feedback:** Stop button progression; replay filter; `/cancel` slash menu entry with `availableWhileStreaming: true`. FR-3a, FR-21, FR-16, FR-26. Tests T13, T14, T15.
10. **Frontend E2E (web only):** stop button + slash menu + Escape + cascade + audit + UI progression. Tests T21–T26.
11. **Tier B channel commands:** dispatch `/stop`, `/cancel` and `/stop-redirect <instruction>` with the same chat-scoped semantics. FR-2, FR-5, FR-29; T11.
12. **CLI Stop parity — Amended 2026-10-06 (founder):** first eligible Esc / `/stop` stops this turn and opens 3 s; second Esc within it stops this chat and its helper tree; `/cancel` is an immediate stop of the chat and its helper tree; redirect continues this chat, root or helper. Delete the old 500 ms mechanism. FR-3, FR-5, FR-31a; T16, T17, T17b.

### Phase 2 — Tier A native registration polish (incremental, ~1 day per channel)

**These FRs improve discoverability but cancel works without them** (text-parsing fallback per FR-28).

13. Slack `/cancel` registration. Test T12b.
14. Discord `/cancel` registration. Test T12c.
15. Teams `/cancel` registration. Test T12d.
16. Feishu `/cancel` registration. Test T12e.
17. DingTalk `/cancel` registration. Test T12f.
18. Google Chat `/cancel` registration. Test T12g.

Each Phase 2 step is independently reviewable and can land sequentially; failure to land all 6 within v0.1 means those channels have text-parsing only (still functional). Re-baseline per F-09: total Phase 2 effort is ~6 days, vs original spec's 1-2 days for all 6.

### Phase 3 — Final verification

19. Full CI run; SC-1 through SC-10 verified; release notes drafted.

**Total estimated effort:** Phase 0 = 1-2h; Phase 1 = 3-5 days (core release-blocking); Phase 2 = ~6 days for full Tier A polish (parallelizable across multiple sessions); Phase 3 = 1 day. **Minimum to unblock v0.1 = Phase 0 + Phase 1 = 3-5 days.**

---

## 17. Out of Scope / Future Work

- **Per-subagent cancel** (cancel one sub-turn while keeping parent running). Defer (tracked; no release scheduled) if requested.
- **`/retry` after cancel.** Defer (tracked; no release scheduled) if usage data shows demand.
- **`/end` session-close command.** Defer; SPA handles session-end.
- **User-scoped cancel authorization** (only the turn initiator can cancel). Defer (tracked; no release scheduled) — the F-08 abuse-detection observability is the v0.1 mitigation; if audit logs show real abuse patterns post-launch, initiator-only mode can be added then as an opt-in channel setting.
- **`/cancel-all` global kill switch** across all user sessions. Defer; out of v0.1 mental model.
- **Cancel-on-disconnect** (auto-cancel on WebSocket close). Considered separately from explicit `/cancel`; not addressed by this spec.
- **Tunable abuse-detection thresholds (FR-25a).** Hard-coded N=10/M=60s in v.1; configurable later (no release scheduled).
- **MCP server-side cancel propagation** beyond the standard context-cancel contract. Tool-author responsibility for v0.1.
