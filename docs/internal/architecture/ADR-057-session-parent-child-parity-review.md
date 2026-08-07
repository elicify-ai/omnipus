# ADR-057 Review — adversarial red-team (v2)

- **Reviewed document:** `docs/internal/architecture/ADR-057-session-parent-child-parity.md` (v2, Proposed, 2026-08-03)
- **Review date:** 2026-08-03
- **Mode:** structured-spec (ADR with labelled decisions D1–D5, risks R-1–R-6, work items 1–9; no BDD/FR-ID/traceability matrix)
- **Grounding:** verified against the live tree on `feature/plan-swimlane-board` @ `0ee87fbe`, via one direct pass plus six parallel exhaustive sweeps (transcript-id consumers, WS frame routing, the durable edge, history replay, the cancel/steering surface, session-store scaling). Every file:line below was opened.

---

## 1. Executive summary

I spot-checked 24 of the ADR's own citations. **All 24 resolve to exactly the construct claimed.** The v1→v2 corrections are real and the document's discipline about *what it examined* is genuinely high. That is not where this fails.

It fails on **selection**. §3 "What this does NOT break (validated)" validates three surfaces. An exhaustive sweep of `turnState.transcriptSessionID` finds **~30 silent consumers and exactly one loud tripwire in production code** (`verifyCallerOwnsSession`, `delegate.go:1975`). The value is not "the FR-6a inheritance plus a hide-filter" — it is the **de-facto subtree-identity key** for cancellation, hard-abort escalation, background-process ownership, tool-approval grants, pending-approval teardown, the ADR-045 watchdog, the cancel pre-arm latch space, media storage, audit attribution and every transcript write. Several of those subsystems were built *specifically to exploit the sharing this ADR removes*, and say so verbatim in their doc comments.

Two of the ADR's load-bearing claims are also wrong on the code:
- **D2/§5-item-2 names a field (`OwnerScopeID`) that is empty for exactly the population the cascade must reach.**
- **R-3 is scoped to "after a browser reload"; the client-side evidence shows it breaks on the live connection, 100% of the time, on the first delegation.**

**Findings: 5 CRITICAL, 14 MAJOR, 5 MINOR/OBSERVATION.**

**Verdict: BLOCK.**

The strategic direction — one execution primitive, delete the special case — is defensible and I am not arguing against it. It is under-scoped by roughly an order of magnitude, and its §9 verification plan is aimed at three risks when the real count is at least ten.

---

## 2. Findings

### CRITICAL

---

#### C-1 — D2 / §5 item 2: `OwnerScopeID` is EMPTY for the first generation of children. The durable walk has no root edge.

**Lens:** Incorrectness · **Sections:** D2, §5 item 2, R-4

The cancel story rests on: *"a walk over the durable edge already being written at delegation time: `OwnerScopeID = ToolDelegateSessionID(ctx)` with `OwnerScopeKind = OwnerScopeParentSession` (`pkg/tools/delegate.go:1119-1122`), plus `ParentAgentID` (`:1149`)."*

The cited code says the opposite for the case that matters:

```go
// pkg/tools/delegate.go:1117-1122
ownerScopeKind := session.OwnerScopeHuman
ownerScopeID := ""
if parentDelegateID := strings.TrimSpace(ToolDelegateSessionID(ctx)); parentDelegateID != "" {
    ownerScopeKind = session.OwnerScopeParentSession
    ownerScopeID = parentDelegateID
}
```

`WithDelegateSessionID` has **exactly one non-test call site**: `pkg/agent/subturn.go:1080`, on a *child* turn's context. A root chat turn's ctx therefore carries no delegate session id, so for **every direct child of a chat turn** `ownerScopeKind = OwnerScopeHuman` and **`ownerScopeID = ""`**. `pkg/session/lifecycle.go:141-143` states it as contract:

> `OwnerScopeHuman` — no single owning id; a top-level chat-goal session owned by the human/chat-principal. **OwnerScopeID is empty.**

Consequences:

1. **A chat-level Stop cannot find its own direct children by `OwnerScopeID`.** The walk has no starting edge. Grandchildren *are* linked (verified: `subturn.go:698-701` → `subturn.go:1075-1080` → `delegate.go:1117` gives `C.OwnerScopeID == B's session id`), so D2 would reach depth 2 and deeper while missing depth 1 entirely — the inverse of a useful failure mode.
2. **`ParentAgentID` cannot stand in.** It is an *agent config id* (`ToolAgentID(ctx)`, `pkg/tools/base.go:203`), not a session id. Two chats where the same agent is delegating are indistinguishable — a Stop in chat A would cancel chat B's in-flight children. `pkg/tools/list_jobs_sources.go:311-315` is explicit that it is a *principal* predicate, not a parentage predicate.
3. **The field that would work is `ParentDurableKey`** (`delegate.go:1106`, stamped from `ToolTranscriptSessionID(ctx)`). Under this ADR it stops being subtree-shared and becomes a genuine strict-direct-parent edge — which is precisely why D5's narrowing works. **The ADR uses this fact in D5 and misses it in D2.** It never proposes `ParentDurableKey` as the walk edge.

**Fix:** Rewrite D2 and §5 item 2 to name `ParentDurableKey`, and state explicitly that `OwnerScopeID` is unusable for generation 1. Re-derive R-4's sizing from `ParentDurableKey == sessionID`, recursed, with the queryability fix from M-4.

---

#### C-2 — R-3 is mis-scoped: in-span tool frames break on the LIVE connection, immediately — not "after a browser reload".

**Lens:** Incorrectness · **Sections:** R-3, §3 "WS span brackets are safe", §5 item 5

R-3 says the frames *"survive on the originating connection (`matchesEvent` matches `ChatID` first…) but **after a browser reload** … they stop matching."*

`matchesEvent` (`pkg/gateway/websocket.go:3007-3018`) decides only whether the gateway **writes the frame to the socket**. It does not decide where the browser **files** it. The client routes strictly by the frame's own `session_id`, with no chat check at all:

```ts
// src/store/chat.ts:2875-2884  (handleFrame)
const frameSessionId = (frame as { session_id?: string }).session_id
...
const targetSid: string | null = (() => {
  if (frame.type === 'session_started') return activeSid
  if (frameSessionId) return frameSessionId      // ← the bucket key
```

`tool_call_start` and `tool_call_result` are both in `SESSION_SCOPED_FRAME_TYPES` (`src/store/chat.ts:1236-1249`), alongside `subagent_start`/`subagent_end`.

So once D1 lands:
- `SubTurnSpawnPayload.SessionID`/`SubTurnEndPayload.SessionID` stay `parentTS.transcriptSessionID` (`subturn.go:1183`, `:1424` — §3 pins them there **deliberately**) → the span registers in bucket `<chatSid>`.
- `ToolExecStartPayload.SessionID`/`ToolExecEndPayload.SessionID` are `ts.transcriptSessionID` (`loop.go:8688`, `:9038`) → become `<childID>` → the steps file into bucket `<childID>`.

**The span and its steps land in two different session buckets on the very first delegation, on the original connection.** Every SubagentBlock renders empty. The correlation chain is `session_id → bucket; parent_call_id → span; call_id → step` (`chat.ts:3588-3618`, `:3805-3874`, `:3964-3969`) — the first hop breaks and the rest never runs. The out-of-order buffer splits identically: its key is `` `${targetSid}:${parentCallId}` `` (`chat.ts:1187`, `:3620`, `:3876`), so a `tool_call_start` arriving before its `subagent_start` is buffered under `childID:…` and looked up under `chatSid:…`, then flushed flat after `ORPHAN_BUFFER_TTL_MS`. The only signal is `logDiagnostic('chatAttachStepSpanIndexMiss', …)` (`chat.ts:1959`) — not user-visible.

This inverts the ADR's risk ranking. R-3 is presented as a rare, reload-only cosmetic issue below R-1/R-2; it is a **100%-reproducible break of the primary delegation UI on the happy path**, and the largest single piece of work in the migration.

**And it hides the bigger contract problem you asked about.** `session_id` on the wire currently means two things at once: *(a)* the client's routing/bucket key and *(b)* the identity of the session that produced the event. FR-6a made them identical by accident. The moment they diverge, **every one of the 20 types in `SESSION_SCOPED_FRAME_TYPES` needs both** — not just the two `ToolExec*` payloads. §5 item 5's "a parent-scoped routing id on in-span tool payloads" is the right shape applied to too small a surface. Related evidence that the frame contract is already strained here: `RateLimitPayload` has **no** `SessionID` field at all (`pkg/agent/events.go:525-533`) and its `session_id` is reconstructed from the stale-chat-id map (`websocket.go:3461`) — while `'rate_limit'` *is* in `SESSION_SCOPED_FRAME_TYPES`, so a reconstructed `""` is **dropped in production** (`chat.ts:2912-2917`).

**Fix:** Re-classify R-3 as CRITICAL; correct §3's "WS span brackets are safe" (true of the gateway, false of the client). Enumerate all `SESSION_SCOPED_FRAME_TYPES` and decide per type whether it carries the routing key, the producing-session key, or both. Add `chat.ts:2880`'s bucketing rule as a named constraint the contract change must satisfy.

---

#### C-3 — Tool-approval grants stop applying inside every delegated child, and pending approvals stop being cancelled. Both silent. Neither is in R-1..R-6.

**Lens:** Insecurity (Availability/Elevation) · Incompleteness · **Sections:** §3, R-list

**(a) Grants.** Keyed on `{sessionID, agentID}`:

```go
// pkg/security/approvalgrants.go:112
func (s *ApprovalGrantStore) Inherit(sessionID, parentAgentID, childAgentID string) {
    parentSet, ok := s.grants[grantKey{sessionID: sessionID, agentID: parentAgentID}]
    ...
    childKey := grantKey{sessionID: sessionID, agentID: childAgentID}
```

- **Write**, at spawn: `al.ApprovalGrants().Inherit(parentTS.transcriptSessionID, parentTS.agentID, agent.ID)` — `pkg/agent/subturn.go:916`.
- **Read**, inside the child's turn: `al.ApprovalGrants().IsAllowed(ts.transcriptSessionID, ts.agentID, toolName)` — `pkg/agent/loop.go:8617`; also `CheckGrantOrRequestApproval(turnCtx, ts.transcriptSessionID, …)` at `:8631`.

Today both resolve to the same id. Under D1, `Inherit` writes under `<chatSid>` and the child reads under `<childID>`. **Every inherited grant misses** (`approvalgrants.go:66-69`: map miss → `return false`). The codebase already warns about exactly this shape at `loop.go:11243-11250`.

The failure direction is safe (re-prompt, not auto-approve) but the *availability* impact is severe: the child falls through to `recordAskPendingToolCall` + `CheckGrantOrRequestApproval` and **blocks on a human for up to 300 s per tool call**. Combined with the project's own rule that `delegate` runs and their SubagentBlocks are hidden from the thread unless verbose chat is on (`src/lib/toolVisibility.ts:218-223` — `shouldRenderSubagentSpan` returns `verboseChatEnabled` and nothing else), the observable symptom is **a delegation that hangs for five minutes with no prompt and no explanation**.

**(b) Pending-approval teardown.** `ToolApprovalRequest.SessionID` is stamped from `ts.transcriptSessionID` (`loop.go:8473`) and the registry filters on it (`pkg/gateway/approvals.go:419`). The cancel hook `cancelAllPendingForSession(sid)` (`websocket.go:1978` → `approvals.go:414`) is called with the *chat* session id. After D1 a chat-level Stop no longer cancels a child's pending approvals, and the entries' 300 s timers (`approvals.go:423-426`) keep running with the agent goroutine blocked on `resultCh`.

**(c) No cleanup.** `CloseSession(sessionID)` clears grants (`pkg/agent/session_end.go:45`) and evicts the loaded-tool manifest (`forgetSession`, `:38`). **Nothing calls `CloseSession` for a child session.** Per-child grant sets, `loadedTools` buckets and `metaCache` entries accumulate for the process lifetime. Note also that `forgetSession` cleans `recallSpans` by a *suffix scan* for `":session:"+sessionID` (`loop.go:11497`) — a heuristic that assumes the transcript id is embedded in the sessionKey, which is exactly what diverges here.

**Fix:** Decide, in writing, whether a child inherits its parent's standing approvals. If yes, `Inherit` must write under the child's key and the security consequence of a grant crossing an isolation boundary must be stated. If no, approval prompts inside invisible delegations become the new normal and need a UX. Either way: add child-session `CloseSession`/`ClearSession`/`forgetSession`, re-scope `cancelAllPendingForSession` to the descendant walk, and give this its own §9 acceptance criterion — it fails silently in both the current and the naive-fixed form.

---

#### C-4 — Two shipped cancel-safety mechanisms are built *on* the shared id and both fail silently when it is removed.

**Lens:** Incorrectness · Inoperability · **Sections:** R-4 (understates), §3 (omits)

**(a) The graceful→hard→detach escalation ladder stops escalating.**

```go
// pkg/agent/cancel.go:462  (PHASE B, 3s → hard)
if len(al.sessionTurnsStillAlive(sessionID)) == 0 { return }
// pkg/agent/cancel.go:487  (PHASE C, 5s → detach)
stillAlive := al.sessionTurnsStillAlive(sessionID)
if len(stillAlive) == 0 { return }
```

`sessionTurnsStillAlive` matches `ts.transcriptSessionID == sessionID` (`pkg/agent/steering.go:745`). Its doc comment (`steering.go:713-737`) documents the exact bug it exists to close — the root finishing gracefully inside the 3 s window while a background delegate keeps running, *"invisibly, for as long as its own task takes (minutes, for a multi-step delegate)"* — and **that fix depends entirely on the child sharing the id**. Remove the sharing and the ladder sees only the root, concludes "already finished", returns, and the child is never hard-aborted or detached. This is not R-4 (which is about the graceful *nudge* fan-out); the escalation gate is a separate mechanism.

Same for `collectDescendantTurnIDs` (`steering.go:429`) — the `turn_canceled` **audit entry's** descendant list silently empties, so the audit trail also stops recording what a Stop reached.

**(b) The ADR-045 orphan watchdog loses its interlock and starts reaping live background work.**
`hasLiveCriticalDelegate(sessionID)` (`steering.go:780-787`) skips on `ts.transcriptSessionID != sessionID`, so it **returns `false` unconditionally** once children carry their own id. It is condition 2 of the watchdog's three-part fire predicate (`orphan_watch.go:277-288`, consulted at `:336`); a permanently-satisfied interlock means the watchdog reaps the root turn while a `Critical:true` async delegate runs alongside it. `orphan_watch.go:279-287`:

> RequestCancel's PHASE B/C escalation … is session-wide by construction and cannot be scoped to "the root only" from the outside — so rather than reap anyway … this mechanism defers reaping ENTIRELY while the delegate survives.

Async delegation hardcodes `Critical: true` (`pkg/tools/delegate.go:1400`), so this is the normal case.

Both failures are **success-shaped**: a predicate returns "nothing to do" and every caller proceeds happily. This is the §9 defect class, twice, undiscovered.

**Fix:** §5 must add "re-base `sessionTurnsStillAlive`, `collectDescendantTurnIDs` and `hasLiveCriticalDelegate` onto the descendant walk" as a first-class item, and §9 needs a criterion each: a real registered root that finishes, a real registered `Critical:true` child that does not, a real Stop — asserting hard-abort *and* detach fire, the audit descendant list is non-empty, and the watchdog does *not* fire.

---

#### C-5 — Deleting the hide-filter un-hides delegate narration in every session already on disk. There is no migration.

**Lens:** Incompleteness · Incorrectness · **Sections:** §2, §5 item 6, §8 Q5

§2 removes `IsDelegateChildEntry()` and its filter sites on the reasoning that *"child content is no longer in the parent's file, so there is nothing to filter."* That is true only of **future** transcripts. Every existing `transcript.jsonl` already contains the child's full narration and final report — confirmed end to end: written via `ts.transcriptSessionID` at `turn.go:1208` (intermediate), `turn.go:1270` (final answer), `websocket.go:4254` (streamed), `external_dispatch.go:463-562` (3P), all tagged `ParentSpawnCallID` (`subturn.go:1054-1056`).

Delete the filter and every historical session **dumps delegate narration, final reports and `[external-cli permission]` lines as top-level main-chat bubbles** on next load. That is verbatim the regression the filter was written to fix — `pkg/gateway/rest.go:818-822`:

> without it, a fresh page load/reopen of a session that included a delegation dumped the delegate's own raw intermediate narration and final report (plus any "[external-cli permission]" lines) as top-level main-chat bubbles that a live reconnect never showed.

§8 Q5 asks about "sessions in flight at deploy time". The bigger problem is **sessions at rest**.

**Also: there are four filter sites, not two.** §2 names `replay.go:298` and `rest.go:823-832`. It misses:
- `pkg/agent/verifier_adjudication.go:406` — `renderTranscriptEntriesForWindow`, i.e. **what the verifier/Judge sees**. Deleting it changes adjudication inputs.
- `pkg/tools/inspect_session.go:172` — the agent-facing `inspect_session` tool.

**And the predicate is content-based, not location-based:**

```go
// pkg/session/daypartition.go:332-333
func (e TranscriptEntry) IsDelegateChildEntry() bool { return e.ParentSpawnCallID != "" }
```

Every entry a child writes carries a non-empty `ParentSpawnCallID` *regardless of which file it lands in*. So **leaving** the filter makes `GET /api/v1/sessions/{childID}` and `inspect_session(childID)` return an **empty transcript** — silently killing §4's headline "Gained" (drill-down with the existing components).

**Fix:** §5 item 6 must carry a migration decision for historical transcripts (keep the filter for pre-cutover entries via a date/schema marker; or a one-shot rewrite; or accept and document the un-hiding). Correct the site count to four and state the predicate's real semantics.

---

### MAJOR

---

#### M-1 — Background shell processes: R-2 is right, and its scope is bigger than stated.

**Lens:** Incompleteness · **Sections:** R-2, §5 item 3

Confirmed exactly as written, with the write site pinned: `ProcessSession.OwnerSessionID` is stamped from `ToolTranscriptSessionID(ctx)` at `pkg/tools/shell.go:571-572` → `shell.go:1035`, and `KillAllForSession` matches on it at `pkg/tools/session.go:455`. `pkg/tools/session.go:95-103` says it outright: *"the **chat/transcript session ID** that owns this background process."*

What R-2 misses: `RequestCancel` fires `hooks.KillBackgroundSessions(sessionID)` **unconditionally and before `ClaimCancel`** (`cancel.go:233-236`), so this is not an escalation-stage concern — a single Stop currently reaps every descendant's detached processes in one call, and after D1 it reaps none of them. And `InterruptBySessionKey` (the `delegate cancel` path) **never calls it at all**, so a per-delegation cancel already leaks; D1 makes that the only remaining reaper.

**Fix:** As R-2 says, plus: state whether `delegate action=cancel` should now also kill that child's background shells (it currently does not, and after D1 nothing else will).

---

#### M-2 — Cancel pre-arm: the ADR deletes an invariant that `cancel_prearm.go` states verbatim as its correctness argument.

**Lens:** Incompleteness · **Sections:** §3, §5

`preArmKeysForTurn(ts)` keys on `ts.transcriptSessionID` (`pkg/agent/cancel_prearm.go:352-361`); a Stop arms under `preArmKeyForScope` → `"s:"+sessionID` (`:336-344`); the pending-spawn marker is set and cleared under the **parent's** identity (`subturn.go:585`, `:1147`); the child consumes at `cancel_prearm.go:602`.

`cancel_prearm.go:384-392` justifies the design in these words:

> spawnSubTurn's own early-return/registration cleanup supplies `(parentTS.transcriptSessionID, parentTS.channel, parentTS.chatID)` — **the same parent identity the child inherits verbatim** … so the SAME keys the marker was set under are the ones cleared.

This ADR deletes the "inherits verbatim" clause. A Stop landing in the pre-registration race is filed under `s:<chatSid>` while the child registers under `s:<childID>`: the latch is never consumed, the child starts and runs, and `notifyLatchExpired` reports the cancel "never landed" after the fact. The `(channel, chatID)` fallback key still matches — but it is the Tier-B fallback only; the web-SPA Stop supplies a session id, so `preArmKeyForScope` returns the session form. There is an existing repro test for this race (`pkg/agent/cancel_async_delegate_repro_test.go`).

---

#### M-3 — D1 collapses the very distinction the #577 fix introduced three days ago, recreating the same defect shape it claims to eliminate.

**Lens:** Inconsistency · Overcomplexity · **Sections:** §1, §2 ("structurally impossible"), D2

Today two functions with **identical Go signatures** take a session id and do different things:
- `InterruptSession(id)` — Range-cascade over `transcriptSessionID` (`steering.go:459`).
- `InterruptBySessionKey(id)` — point `activeTurnStates.Load(id)` (`steering.go:616`), written deliberately **not** to cascade, so `delegate cancel` targets one delegation without hitting the parent or siblings (`steering.go:575-594`, the #577 fix).

After D1, `sessionKey == transcriptSessionID` for every turn, so **both functions take the same id** and differ only in cascade semantics. `delegate.go:583-592` (`SetCancelHooks`) already flags this hazard by name: *"The two pairs share the same Go signature … so the compiler cannot catch a re-wire."*

The ADR claims the dual-namespace confusion class "becomes structurally impossible". It removes one instance and creates another of the same shape — and the new one sits on the cancel path, in the code #577 just fixed. Alternative A (named ID types) is proposed as a *companion*; on this evidence it is a **precondition**, because after unification the type system is the only thing left that can distinguish these two calls.

---

#### M-4 — The descendant walk is not expressible against the store, and is O(all sessions ever) per hop — inside a 3-second cancel window.

**Lens:** Infeasibility · **Sections:** D2, §5 item 2

`LifecycleFilter` has exactly five fields — `WorkspaceID`, `AgentID`, `ParentAgentID`, `States`, `NonTerminalOnly` (`pkg/session/lifecycle.go:539-561`). **No `OwnerScopeID` field, no `ParentDurableKey` field.** `lifecycle.go:571-576` documents that `ParentDurableKey` is *explicitly never matched on*. "Children of session X" cannot be expressed against the store API at all.

And `List` has no index:

```go
// pkg/session/lifecycle.go:625-644
ids, err := s.scanSessionIDs()   // os.ReadDir over the WHOLE session_lifecycle dir
for _, id := range ids {
    rec, err := s.Load(id)       // opens + JSON-parses EVERY line of EVERY file
```

A transitive walk is therefore **one full-directory scan plus a full file parse per record, per depth level**, on the Stop path inside `RequestCancel`'s 3 s budget — concurrently with M-8's fsync-bound create storm. The one existing single-level consumer already needs a truncating `applyScanCeiling` to survive it (`pkg/tools/list_jobs_sources.go:338`).

§5 item 2 is sized as "a durable descendant walk". It is a new secondary index + a new filter field + a bounded-latency guarantee.

---

#### M-5 — Five coverage holes in the durable record. Each becomes an uncancellable orphan under D2, silently.

**Lens:** Incompleteness · **Sections:** D2, D3, R-4

`LifecycleRecord` has exactly **two** construction sites in the non-test, non-generated tree: `pkg/tools/delegate.go:1166` and `pkg/agent/task_executor.go:228`. That leaves:

1. **Plan owner + supervision sessions mint no record at all.** `mintPlanSession` (`pkg/agent/plan_engine.go:3951-3975`) calls `UnifiedStore.NewSession` only. D3 is right that plan cancel is unchanged — but it means the lifecycle store is *not* a complete session graph, so D2 cannot be the single mechanism.
2. **Task-dispatch sessions deliberately have an empty `ParentAgentID`** (`task_executor.go:202-208`) and put a **plan id** in `OwnerScopeID` (`:222-234`). A `ParentAgentID` walk skips them; an `OwnerScopeID` walk mistakes a plan id for a session id.
3. **The mint is skipped when `t.lifecycle == nil`** (`delegate.go:1121`), wired conditionally at `pkg/agent/session_messaging_wire.go:140-143`. In any boot without it, D2's walk finds zero descendants and cancels nothing, with no error.
4. **`tools.delegate.require_parent_agent_id=false`** mints records with a blank `ParentAgentID` (`delegate.go:1148-1159`).
5. **External-CLI (3P) children's own sub-delegations never reach the mint** — they run outside the Omnipus tool surface.

Also note the key-space collision this creates: `RequestCancel` writes `TransitionSession(lifecycleStore, store, sessionID, LifecycleCancelled, "")` keyed by the **transcript** id (`cancel.go:428`), while delegate records are keyed by `delegateSessionID`. Today those are different namespaces; after D1 a chat-level Stop would write a lifecycle transition for a chat session that has no lifecycle record.

---

#### M-6 — `AppendTranscript` silently succeeds into an orphan directory. This is what will make the migration look green.

**Lens:** Inoperability · **Sections:** §9, §5 item 1

```go
// pkg/session/unified.go:802-823 (AppendTranscript, unknown session id)
// validateSessionID passes any UUID (unified.go:230-236)
// AppendJSONL MkdirAll's the directory (pkg/fileutil/file.go:207-210)
// the entry is written; readMetaLocked then fails
// → slog.Warn "could not update meta stats"  → return nil
```

And `ReadTranscript` on a missing path returns `[]TranscriptEntry{}, nil` (`unified.go:1192-1194`).

Consequence: if work item 1's exact-id create is missed on **any** path — or races the first write, or is skipped in a code path that constructs a child turn without going through `spawnSubTurn` — the child's entire transcript lands in `<store>/<childUUID>/transcript.jsonl` with **no `meta.json`**, which means:
- invisible to `ListSessions`, to `replay.go`, and to `GET /api/v1/sessions/{id}` (which 404s without meta — `pkg/gateway/rest.go:834-844`, via `ResolveSessionStore`'s `GetMeta` probe at `pkg/agent/loop.go:5012-5039`);
- **every write returns `nil`**, so no test that asserts "the append succeeded" catches it.

The four transcript writers in `turn.go` (`:1130`, `:1208`, `:1270`, `:1325`) all guard only on *empty*, not on *resolvable* — and `appendErrorTranscript`'s explicit "suppressed, will NOT appear in replay" warning (`turn.go:1302-1312`) fires **only** for the empty case, so a bogus non-empty id skips the warning entirely.

**Fix:** Make `AppendTranscript` fail loudly on an unknown session id (or add an explicit `MustExist` variant used by the turn writers), *before* the migration, and add an acceptance criterion asserting the child's `meta.json` exists and `GET /api/v1/sessions/{childID}` returns 200 with non-empty messages.

---

#### M-7 — Six further silent consumers not in any risk item.

**Lens:** Incompleteness · **Sections:** §3, R-list

| Consumer | Site | Silent failure |
|---|---|---|
| `sessionActiveAgent` / `activeAgentResolver` | `loop.go:6653-6654` (`resolverKey := "session:" + opts.TranscriptSessionID`) | Load miss → `return ""` → falls back to the turn's starting agent; wrong `agent_id` stamped on entries after a handoff |
| Sysagent ownership (`WithSessionOwner`) | `loop.go:6844-6848` | `ResolveSessionStore`/`GetMeta` fail → the `if` never executes → `system.workspace.create`/`system.task.create` stamp **no owner**. SEC-2/#406 rule 2 becomes a no-op with zero signal |
| Tool-media uploads | `pkg/tools/normalization.go:247` → `pkg/media/tempdir.go:33-51` | A UUID passes every guard, so files land in `<home>/uploads/<childUUID>/` with `CleanupPolicyForgetOnly` — **immune to the TTL cleaner AND to session cascade-delete**. Permanent disk leak |
| Async delegate result persistence | `loop.go:8776` → `async_notifier.go:285` → `loop.go:6549` | `ResolveSessionStore` nil → falls back to the unscoped `agent:<id>:main` history bucket, the cross-session-contamination case documented at `loop.go:6577-6596` |
| External-CLI consent scope | `pkg/agent/external_dispatch.go:380` (`ConsentDispatcher(…, childTS.transcriptSessionID, …)`) and the in-place tool-call update at `:609`/`:646` | 3P consent/permission scope changes; `mutateToolCallInTranscript` returns false → silent duplicate append at `:617-624` |
| `repairHistory` | `loop.go:7389-7390` → `pkg/agent/repair.go:79-86` | One Warn ("Transcript unavailable; orphans will be dropped at the wire"), then returns unrepaired |

Plus audit-attribution drift (cosmetic but it is the audit trail): `pkg/tools/list_jobs.go:541`, `pkg/tools/path_audit.go:161`/`:208`, `pkg/tools/memory.go:180`/`:527`, `pkg/tools/plan_correct.go:335`, `pkg/sysagent/tools/task.go:1095`.

---

#### M-8 — R-5 is understated by an order of magnitude: it is a global-lock contention regression, not a directory count.

**Lens:** Incompleteness · Infeasibility · **Sections:** R-5, §8 Q3

R-5 says "24 directories". What `createSessionLocked` actually costs, per call, **holding the store-wide write lock**:

- `UnifiedStore.mu` is a single, non-striped `sync.RWMutex` (`pkg/session/unified.go:161`); `NewSession` takes the **write** lock for the whole create (`:415-416`).
- Inside it: `os.MkdirAll` (`:462`) + `writeMetaLocked` → `fileutil.WithFlock` + atomic write (`:466`, `:786-799`) + a second atomic write for the empty `transcript.jsonl` (`:470-475`).
- `fileutil.WriteFileAtomic` does `tmpFile.Sync()` (`pkg/fileutil/file.go:97`) → rename → **parent-directory `Sync()`** (`:121`). Two fsync-and-rename cycles plus a directory fsync, per session.
- The same lock is taken by `AppendTranscript` on **every streamed line** (`unified.go:810-811`, held across the append *and* a full meta-stats rewrite at `:848`) and by `ListSessions` (`:1248`, also the write lock).

A 24-way fan-out serializes 24 fsync-bound creates and **stalls token streaming in every other session in the store**. `UnifiedStore` is the only store in `pkg/session` that did not get the 64-shard treatment (cf. `pkg/session/lifecycle_lock.go:22-36`, `pkg/session/message_inbox.go:135-137`). And delegation has **no admission control at all**: `AdmissionController` (`pkg/agent/admission.go:20-32`) gates only the bus dispatcher's `sessionWorkers`; `spawnSubTurn` bypasses it (documented at `cancel_prearm.go:778-780`).

The listing/UI side is worse than "needs to treat these as subordinate":
- `SessionMeta` has **no parent field** (`pkg/session/daypartition.go:76-104`); the wire `Session` type has none either.
- `SessionType` (`unified.go:26-57`) has no subordinate value. The only precedent for hiding a type is `verifier` (`rest.go:783-785`), which needed a store enum + an OpenAPI enum + an SPA change.
- `GET /api/v1/sessions` has **no `limit`/`offset`/cursor at any layer** — store (`unified.go:1247`), aggregator (`loop.go:5046`), REST (`rest.go:758-812`), client (`src/lib/api.ts:1379-1388`).
- The sidebar hardcodes `maxVisible = 9` sorted by recency (`src/components/layout/Sidebar.tsx:450-457`), so 24 fresh child sessions **evict the parent chat itself** behind a countless "More…". `SearchModal` renders the full list unvirtualized (`src/components/search/SearchModal.tsx:649`).
- `RetentionSweep` walks every session directory recursively (`pkg/session/retention_sweep.go:35`).

**Fix:** Re-classify R-5 as a performance/contention risk with a measured budget. Add to §5: stripe `UnifiedStore.mu` (or use a create path that does not hold it across fsync), a subordinate marker on `SessionMeta` + `SessionType` + the OpenAPI enum, pagination, a sidebar filter. **Consider a new Alternative D: mint the child's `UnifiedMeta` lazily on first drill-down**, so the fan-out cost is paid only when someone looks.

---

#### M-9 — §7's supersede is under-scoped: it silently changes D16, and the epic it reverses is still open.

**Lens:** Inconsistency · **Sections:** §7

ADR-053 **D16** — *"Ad-hoc `delegate` inboxes are keyed to the **durable chat/plan id**… Inbox survives a parent Stop/Play"* — is implemented as:

```go
// pkg/tools/message_parent.go:326-331
// ownerKeyFor resolves the DURABLE chat/plan id (D16) this child's parent inbox is keyed to.
// ParentDurableKey (not OwnerScopeID) is authoritative here…
func ownerKeyFor(rec *session.LifecycleRecord) string { return strings.TrimSpace(rec.ParentDurableKey) }
```

`ParentDurableKey` is `ToolTranscriptSessionID(ctx)` at `run` time (`delegate.go:1106`). Under this ADR that becomes the **immediate parent's** session id, so a grandchild's `message_parent` output routes to the *child's* inbox rather than the chat's. That may be more correct — but D16's stated property changes and §7 claims to supersede only D1. The producer/consumer pair (`message_parent.go:640` `Append(ownerKeyFor(rec))` vs `delegate.go:2009` `Drain(callerOwnerKey(ctx), …)`) must move together or `delegate action=inbox` returns a clean, empty success payload forever. D5's gate and D15's per-child ceiling ride the same key.

Separately: the ADR-053 D1 amendment is 3 days old and the **epic is not closed** — the 2026-07-31 UAT produced 12 defects (#576–#588) whose fixes are still landing on this branch (`0ee87fbe`, `b120c8c2`, `ac7d6d74` are all delegate/`message_parent` fixes from the last few commits).

**Fix:** Enumerate every ADR-053 decision whose *implementation* depends on `ParentDurableKey` or the shared transcript id (at minimum D1, D5, D15, D16) and state the new value for each. Add a sequencing decision: before or after the #576–#588 wave closes?

---

#### M-10 — §3's steering validation is correct for one path and is presented as a coverage claim.

**Lens:** Incompleteness · **Sections:** §3

The direct-tool steering claim checks out: producer `EnqueueSteeringMessage(sessionID, …)` (`steering.go:218`) is called with the bare delegate session id from `delegate.go:2123` (steer) / `:2280` (respond); consumer drains `ts.sessionKey` (`loop.go:7223`, `:7227`, `:8234`, `:9138`, `:9211`); binding at `subturn.go:698`, `:1021`. Fine.

But the **bus-delivered** parent→child steer builds a different scope shape:

```go
// pkg/agent/session_messaging_wire.go:475-480
scope := childSessionID
if childAgentID != "" { scope = "agent:" + childAgentID + ":" + childSessionID }
return al.DeliverSessionMessage(ctx, scope, childAgentID, evt.Message)
```

A delegated child's `sessionKey` is the **bare** `childID` (`subturn.go:1021`), never `"agent:<id>:<uuid>"`. That path pushes into an orphan scope **today** — a pre-existing defect this ADR's §3 would have surfaced had the sweep been exhaustive.

§3 also omits, entirely: `GetActiveTurnHookForSession`'s H1 root-preference (`turn.go:514-543` — which exists *because* "multiple turns share the same transcriptSessionID" and becomes dead code); `resolveSessionIDByChannelChat` (`turn.go:545-583` — Tier-B `/stop` from a channel, which would now return a **child's** own id on the surviving-child-only path); `getActiveRootTurnStateForSession` (`turn.go:587+`); and everything in C-3, C-4, M-2, M-7.

**Fix:** Replace §3 with a complete enumeration of `transcriptSessionID` reads, each classified unaffected / breaks-loudly / breaks-silently. The input is one command: `rg -n "transcriptSessionID" --glob '!*_test.go' pkg/`.

---

#### M-11 — Deleting `NoHistory: true` and the resulting `loadedTools` re-bucketing are asserted as pure wins, with no analysis.

**Lens:** Ambiguity · Incompleteness · **Sections:** §2, §5 item 1

1. **`follow_up` warm resume reuses `childID` verbatim** for the next generation (`subturn.go:1114-1120`; `spawnCorrectiveFollowUp` in `delegate.go`). With `NoHistory: false` and a real session behind that id, generation N+1 would load generation N's history. That may be the point — but the ADR does not say, and "we deleted a flag" is not a specification of resume semantics.
2. **`manifestSessionID(transcriptID, sessionKey)` prefers `transcriptID`** (`pkg/agent/tool_manifest.go:20-25`). Today a child shares the parent's `loadedTools` bucket; afterwards every child starts with an empty progressive-disclosure set and must `load_tool` everything itself — a per-delegation token-cost and latency change. Combined with C-3(c)'s missing `CloseSession`, it is also an unbounded map.

---

#### M-12 — Work item 9 (throttle unification) is unrelated scope with an internally inconsistent premise.

**Lens:** Overcomplexity · Inconsistency · **Sections:** §5 item 9, §8 Q4

The gap is real and correctly cited (`turn.go:150`, `subturn.go:1051`, `task_executor.go:58-60`, `:453`, `:1944` all verified). But:

- **It is orthogonal.** Nothing in D1–D5 touches concurrency. Folding it in means a regression in either mechanism is attributed to the other during bisection — in a migration whose own §9 says silent failure is the expected mode.
- **The premise is overstated.** "the semaphore is never set on a root turn" is true, but `childTS.concurrencySem = make(chan struct{}, rtCfg.maxConcurrent)` at `subturn.go:1051` means **nested** delegation *is* gated. The gap is root-level only; the phrasing reads as though the whole mechanism is dead.
- §8 Q4 then re-opens the semantics ("block-with-timeout vs try-and-refuse") that item 9 asserts should be resolved.

**Fix:** Split into its own issue, referenced here as a dependency only if M-8's fan-out volume makes it a blocker.

---

#### M-13 — R-1's fix is one line; the surrounding state is in-memory-only and never freed.

**Lens:** Incompleteness · **Sections:** R-1, §3 (`list_jobs`), §5 items 4 and 7

`recentActivityLines(task.SessionID, …)` (`delegate.go:1823`) and its documented silent-nil (`:1846-1850`) are correct as written. But:

- `t.tasks` and `t.sessionIndex` are **in-memory only, never serialized, never deleted** — no `delete(t.tasks` or `delete(t.sessionIndex` exists anywhere. Both grow monotonically for the process lifetime.
- **`executeSync` never registers a `DelegateTaskState` at all** — only `executeAsync` does (`delegate.go:1315-1330`). So `status`'s activity snapshot is *already* absent for every synchronous delegation, and `ResolvableSessionIDs` (`:1603-1618`) already returns false for every session after a restart (its own doc says so at `:1595-1597`).

This qualifies §3's `list_jobs` verdict: "`actionable` resolves through `ResolvableSessionIDs`… which D1 preserves" is true, but `actionable` is already `false` for sync delegations and after any restart — so "same rows before and after" is correct while guaranteeing less than it sounds.

---

#### M-14 — The ActivityPanel fallback the project relies on for hidden delegations is partly non-functional today.

**Lens:** Incompleteness · **Sections:** R-3 mitigation, §4

Both C-2's user impact and the project's tool-visibility policy assume ActivityPanel is the durable inspection surface for hidden delegate work. Two of its inputs have **no server-side producer at all**: `grep -rn "subagent_message\|subagent_state" --include=*.go` over the whole repo returns **zero** hits. The frame structs exist (`pkg/api/generated/asyncapi_types.gen.go:496-507`, `:521-532`), the contract defines them (`contracts/asyncapi.yaml:88-91, 381, 393, 914-922`), the SPA consumes them (`src/store/chat.ts:4572-4588` → `src/store/sessionActivity.ts:112`) and `useRunningActivity.ts:495-510` joins them onto every span — but nothing emits one, and they are absent from the `WsFrameType` enum on both sides. `lifecycleState`, `sessionMessages` and `steeringReceipt` on `AgentActivityItem` are therefore permanently `undefined`. `SubagentSpanTerminal.finalResult` (`chat.ts:100`) is likewise never populated by the backend.

This is pre-existing, not caused by the ADR — but it means R-3's implicit fallback ("the ActivityPanel still shows it") does not hold, which raises C-2's severity.

---

### MINOR / OBSERVATION

**m-1 (MINOR) — "This is a net deletion" (§2) does not survive §5.** Additions required by the findings above: an exact-id create wrapper; a `MustExist` transcript-append variant (M-6); a new lifecycle filter field + secondary index (M-4); a durable descendant walk; a shell-kill cascade; a wire-contract change through Constraint #8's 5-step pipeline across up to 20 frame types (C-2); approval-grant re-keying + pending-approval re-scoping + child `CloseSession` (C-3); pre-arm re-keying (M-2); a historical-transcript migration (C-5); a drill-down surface; a subordinate session type + pagination + sidebar filter + lock striping (M-8); named ID types as a precondition (M-3); throttle unification. Four deletions against roughly fifteen additions. Say "a simplification that costs a large migration".

**m-2 (MINOR) — D5 is an open question presented as a decision.** "OPEN — operator decision… Default if undecided: narrow (fail-closed)" sits inside §2 and is repeated as §8 Q1. Resolve it or move it out of the Decision section. The narrowing itself is *correct* on the code — verified: `verifyCallerOwnsSession` has exactly the six call sites claimed (`delegate.go:2010, 2107, 2159, 2321, 2459, 2592`), and `caller == rec.ParentDurableKey` holds at every depth after the change.

**m-3 (MINOR) — §9's bar is stated for R-1/R-2/R-3 only.** R-4, R-5, R-6 get no criterion, and R-6 is self-labelled INFERRED. Given C-3/C-4/M-2/M-6/M-7 add at least eight more silent modes, the rule should be stated as applying to the whole enumerated set, with M-10's sweep as its input.

**m-4 (OBSERVATION) — an unclaimed benefit the ADR should take credit for.** `HydrateAgentHistoryFromTranscript` (`pkg/agent/attach_hydrate.go:42`) reads the parent's transcript **without** the `IsDelegateChildEntry` filter, and runs on every reload (`websocket.go:2577`) and on the self-heal path (`loop.go:6204`). Because a self/untargeted delegation runs with `execSource.ID == the parent's own agent id` (`subturn.go:776-779`, `:841-842`), the delegate's raw narration is currently absorbed into the **parent agent's own LLM context** on reload. This ADR fixes that as a side effect. It is worth stating — both because it strengthens the case and because it is a *behaviour* change to the parent's context that reviewers should see coming.

**m-5 (OBSERVATION) — citation accuracy is a genuine strength; treat it as the floor.** 24/24 spot-checked citations resolved exactly. The failure mode is not fabrication, it is enumeration. Note also that `pkg/agent/message_parent_real_context_test.go:16-17` already warns that its fixture *"happens to make `ToolTranscriptSessionID`"* equal the seeded id — i.e. an existing test would **not** catch a divergence introduced here.

---

## 3. Structural assessment

| Check | Result |
|---|---|
| Every decision (D1–D5) has a stated consequence | **Partial** — D4 has no consequence and no work item; it restates existing behaviour |
| Cross-references internally consistent | **FAIL** — D2 cites `OwnerScopeID` as the parent edge; D5 cites `ParentDurableKey` for the same relationship. Only the latter is usable (C-1) |
| Scope boundaries explicit (in/out) | **FAIL** — item 9 is out-of-scope work inside the work list (M-12); R-6 is a risk deferred to Alternative C |
| Risks have measurable acceptance criteria | **FAIL** — 3 of 6, and the real risk count is ≥ 14 |
| Failure/error scenarios addressed per decision | **FAIL** — D1's failure modes (C-2, C-3, C-4, C-5, M-2, M-6, M-7) are absent |
| Affected-surface enumeration | **FAIL** — 3 surfaces validated; ~30 silent consumers exist |
| Dependencies between decisions identified | **PASS** — the D1→D2→D5 chain is clear |
| Supersede relationships stated | **Partial** — D1 named; D16 changed silently (M-9) |
| Migration plan (in-flight AND at-rest state) | **Missing** — §8 Q5 raises in-flight and stops; at-rest is C-5 |

---

## 4. Test / verification assessment

**§9's principle is right** — real store-backed state, real registered turns, no argument-recording spies; the `plan_engine.go:3937-3944` precedent is apt. The plan is aimed at the wrong three risks.

1. **M-6 first.** `AppendTranscript` returning `nil` after silently MkdirAll'ing an orphan directory (`unified.go:802-823`) is the mechanism by which this whole migration can pass a green suite while losing data. Fix the primitive before writing any acceptance criteria against it.
2. **No criterion for the newly-found silent failures** — C-3 (grants, pending approvals), C-4a (escalation ladder + audit descendants), C-4b (orphan watchdog), M-2 (pre-arm), M-7 (six more). All are `predicate returns "nothing to do"` shapes.
3. **R-3's criterion is aimed past the real failure.** §9 says exercise it "with an actual reconnect". Per C-2 the break precedes any reconnect: the criterion must assert **client-side bucket membership on the live connection** (the `<chatSid>` bucket contains the span *and* its steps), not frame delivery.
4. **No concurrency criterion.** M-8's lock contention and M-4's per-hop scan only appear under fan-out. A 24-way delegation with concurrent streaming in a second session must be an explicit scenario.
5. **No negative-path criterion for M-5's holes** — e.g. "delegate with `t.lifecycle == nil`, Stop, assert the child is cancelled or the operator sees an error".
6. **Existing gate tests that encode the current contract and must be deliberately inverted, not deleted quietly:**
   `pkg/agent/subturn_test.go:2095` `TestSubTurnInheritsTranscriptSessionID` (the FR-6a T0 gate, asserts equality at `:2143-2145`); `pkg/agent/approval_grant_delegation_test.go:19,229` (pins the `Inherit`/`IsAllowed` pairing — C-3); `pkg/agent/cancel_orphan_delegate_test.go:57-79`; `pkg/agent/cancel_subagent_cascade_test.go:51-101`; `pkg/agent/cancel_session_isolation_test.go:12`; `pkg/agent/orphan_watch_test.go:14,223-229`; `pkg/agent/steering_test.go:1693,1765-1811,1865`; `pkg/agent/interrupt_by_session_key_test.go:9-19,232` (pins the two-namespace split — M-3); `pkg/agent/subturn_transcript_nesting_test.go:9-10,93-94`; `pkg/gateway/cancel_subagent_cascade_test.go:5`; `pkg/gateway/replay_test.go:1549`; `pkg/agent/cancel_async_delegate_repro_test.go` (M-2). Roughly 71 test files and ~430 references touch this value.

---

## 5. STRIDE summary

| Component | Threat | Note |
|---|---|---|
| `verifyCallerOwnsSession` / D5 | **Elevation — reduced** | Narrowing chat-subtree-wide → direct-parent is a genuine improvement. Verified at all depths |
| Approval-grant store + pending-approval registry | **Availability / Elevation** | C-3. Fail direction safe (re-prompt); impact is a 300 s invisible block per tool call, and Stop no longer cancels a child's pending approvals |
| Cancel path (`RequestCancel`, escalation, watchdog) | **Availability / Repudiation** | C-4. A Stop reporting success while children keep burning tokens is both availability and audit-integrity — `collectDescendantTurnIDs` silently empties the `turn_canceled` audit descendant list |
| Background shell sessions | **Availability** | M-1. Orphaned detached processes survive Stop; `delegate cancel` already never reaps them |
| Session store (`UnifiedStore`) | **Denial of Service** | M-8. Uncapped session creation (delegation bypasses `AdmissionController`) behind one global fsync-holding mutex is a self-inflicted resource-exhaustion path |
| Tool-media uploads | **Availability (disk)** | M-7. `<home>/uploads/<childUUID>/` is immune to both the TTL cleaner and session cascade-delete |
| Sysagent ownership stamping | **Elevation** | M-7. `WithSessionOwner` silently not installed → SEC-2/#406 rule 2 unenforced with zero signal |
| Historical transcripts | **Information Disclosure** | C-5. Deleting the filter exposes delegate narration (including `[external-cli permission]` lines) as top-level content in every existing session |
| WS frame routing | **Integrity** | C-2 is correctness, not security — but splitting `session_id`'s two meanings warrants one explicit check that a frame cannot be filed against a session the viewer is not authorised for |

---

## 6. Unasked questions

1. **Which field is the parent→child edge?** The ADR answers `OwnerScopeID`; the code answers `ParentDurableKey`. Pick one in writing and prove it for depth 1, 2 and 3 (C-1).
2. **What does `session_id` mean on the wire — routing key or producing session?** It cannot keep meaning both once they diverge (C-2).
3. **Does a delegated child inherit its parent's standing tool approvals?** No position is stated; both answers have consequences (C-3).
4. **What happens to the transcripts already on disk?** (C-5.)
5. **After this change, what is `transcriptSessionID` FOR?** If it always equals `sessionKey`, it is a redundant field and Alternative A's named types become the obvious follow-on deletion. If it does not, say when it differs — and how `InterruptSession` vs `InterruptBySessionKey` stay distinguishable (M-3).
6. **Who calls `CloseSession` for a child session?** Nothing does today, and afterwards children own per-session state (grants, `loadedTools`, `metaCache`, uploads dirs, `recallSpans`).
7. **What is the Stop-path latency budget?** M-4's walk runs unindexed inside a 3 s escalation window, alongside M-8's create storm.
8. **Does this land before or after the #576–#588 fix wave closes?** (M-9.)
9. **Is there any per-session resource left un-enumerated?** Verified as *not* session-keyed and therefore unaffected: browser contexts (always the literal `"default"`, `pkg/tools/browser/tools.go:46`), `web_serve` (`byToken`, `pkg/agent/served_subdirs.go:43`), MCP (per-agent, no session keying), file watchers (none). Verified as session-keyed **but not wired into cancel today**, so they inherit the same divergence: `scheduledProcRegistry.pids` (`pkg/gateway/schedules.go:444`), `idleTickers` (`loop.go:388`), `steerRateWindows`/`sessionIndex` (`delegate.go:1330`, `:1927-1938`).
10. **Is the `follow_up` warm resume supposed to see the previous generation's history now?** (M-11.)

---

## 7. Verdict

**BLOCK.**

Two load-bearing claims are wrong on the code (C-1, C-2). The change breaks five shipped safety mechanisms built specifically to exploit the property being deleted (C-3 ×2, C-4 ×2, M-2) plus un-hides content in every existing transcript (C-5) — and every one of those fails in the success-shaped way §9 itself names as this project's signature defect class. The store primitive that would let you detect any of it returns `nil` on a miss (M-6).

Answering your five targeted questions directly:

1. **Are §3's validations sufficient?** No. They cover 3 surfaces; ~30 silent consumers exist. Missed paths include steering's *bus* variant (M-10), the whole cancel-escalation family (C-4), pre-arm (M-2), approvals (C-3), and six more (M-7).
2. **Is R-3 correctly scoped?** No, on both counts. It breaks on the live connection, not only after reload (C-2), and it does hide a bigger contract problem — `session_id` conflates routing key and producing session across all 20 session-scoped frame types.
3. **Is the `OwnerScopeID`/`ParentAgentID` edge sufficient?** No. `OwnerScopeID` is empty for generation 1, `ParentAgentID` is an agent not a session, the store cannot be queried in that direction, the walk is O(N) per hop unindexed, and five populations have no record at all (C-1, M-4, M-5). `critical=true` children — which are *every* async delegation — are exactly the population it must reach and the one it reaches worst.
4. **Does §7's supersede hold?** Partially. Reversing D1 is legitimate and honestly argued. But it also changes D16 without saying so, moves the ground under D5/D15, and lands inside an open epic whose UAT defects are still being fixed on this branch (M-9).
5. **Are R-1/R-2 the only silent failures?** No. At minimum: approval grants, pending-approval teardown, the hard-abort escalation gate, the audit descendant list, the ADR-045 watchdog, the pre-arm latch, `sessionActiveAgent`, `WithSessionOwner`, tool-media uploads, async-result persistence, external-CLI consent, `recallSpans`, and `AppendTranscript` itself. Thirteen more, all success-shaped.

The single highest-value revision is mechanical: **enumerate every read of `transcriptSessionID` and classify it**, then rebuild §3 from that enumeration instead of from three sampled surfaces.

Address the findings above, then re-run:

```
/grill-spec docs/internal/architecture/ADR-057-session-parent-child-parity.md
```
