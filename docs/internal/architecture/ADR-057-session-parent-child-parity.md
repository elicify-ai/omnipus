# ADR-057: Unify delegate sub-turns onto the own-session execution path

- **Status:** Proposed (v3 — rewritten after `/grill-spec` returned **BLOCK** on v2: 5 CRITICAL, 14 MAJOR, 5 MINOR)
- **Date:** 2026-08-03
- **Related:** [ADR-053](ADR-053-unified-goal-plan-subagent.md) D1/D5/D15/D16 (§8); [ADR-052](ADR-052-autonomous-agent-plan-execution.md) §6.4(a); [ADR-045](ADR-045-orphaned-foreground-turn-timeout.md) (the watchdog interlock, D4); [ADR-056](ADR-056-background-job-visibility.md) (`list_jobs`, the cut `shell` kind); [ADR-036](ADR-036-consolidate-shell-and-subagent-tools.md) (`bash` background sessions); `docs/internal/specs/cancel-cross-channel-spec.md` FR-6a
- **Deciders:** Operator (Daniel Piatkowski)
- **Evidence level:** 1 — direct codebase verification. Every claim below carries a `file:line` citation verified against `feature/plan-swimlane-board` on 2026-08-03 (v2 @ `e9517d1e`). Claims that could not be verified by reading code are tagged **[INFERRED]**.

> **v3 changelog — what v2 got wrong.**
>
> v2 was **BLOCKed**. Its §3 was titled *"What this does NOT break (validated)"* and validated **three** surfaces. An exhaustive sweep — `rg -n "transcriptSessionID" --glob '!*_test.go' pkg/` returns **116 references across 18 files**, plus **19** `ToolTranscriptSessionID(ctx)` call sites — finds roughly **thirty** consumers, of which exactly one fails loudly. v2 validated 10% of the surface and presented it as coverage. Specifically:
>
> 1. **D2 named the wrong field.** v2 built the cancel cascade on `OwnerScopeID`. That field is `""` for **every direct child of a chat turn** — the code says so in three places (`pkg/tools/delegate.go:1117-1122`, `pkg/session/lifecycle.go:141-143`, `:229`). v2's own D5 used the right field (`ParentDurableKey`) for the same relationship in the same document. **See D3.**
> 2. **R-3 was mis-scoped by an order of magnitude.** v2 said in-span tool frames break *"after a browser reload"*. The SPA buckets **strictly** by the frame's own `session_id` with no chat check (`src/store/chat.ts:2885`), so the span and its steps split into two buckets on the **first delegation, on the live connection**. This is a 100%-reproducible break of the primary delegation UI on the happy path. **See D2 and W5.**
> 3. **v2 claimed "this is a net deletion".** Withdrawn. Four deletions against ~20 additions. This is **a simplification of the identity model that costs a large migration**. §6 lists 22 work items.
> 4. **Five shipped safety mechanisms were built *specifically to exploit* the property v2 deletes**, and say so verbatim in their doc comments: the cancel escalation ladder (`pkg/agent/steering.go:713-737`), the ADR-045 watchdog interlock (`pkg/agent/orphan_watch.go:279-287`), the cancel pre-arm latch (`pkg/agent/cancel_prearm.go:385-389`), approval-grant inheritance (`pkg/agent/subturn.go:916` ↔ `pkg/agent/loop.go:8617`), and background-shell reaping (`pkg/agent/cancel.go:233-234`). All five fail **silently** — a predicate returns "nothing to do" and every caller proceeds happily. **See D2, D4, D5.**
> 5. **v2 said the hide-filter has two sites. It has four** (`pkg/gateway/replay.go:298`, `pkg/gateway/rest.go:826`, `pkg/agent/verifier_adjudication.go:406`, `pkg/tools/inspect_session.go:172`) — and deleting it un-hides delegate narration in **every session already on disk**. v3 does not delete it. **See D6.**
> 6. **One v2 citation was simply wrong** (the review's 24/24 spot-check did not include it): v2's R-3 cited `matchesEvent` at `pkg/gateway/websocket.go:2968-2978`. That range is a *different* closure, `matchesChatID`. `matchesEvent` is at `:3007-3018`.
> 7. **v2's §9 verification plan named criteria for three risks.** The real count is fifteen. §10 is rewritten from scratch on the principle that *this migration's failure modes are overwhelmingly silent*, and that a green test suite currently proves almost nothing because `UnifiedStore.AppendTranscript` **`MkdirAll`s an orphan directory, writes the line, and returns `nil`** for a session id that does not exist (`pkg/session/unified.go:814-823`).
>
> **What v3 keeps from v2:** the strategic direction (one execution primitive, delete the special case), the ADR-053 D1 supersede, and D9's trigger-policy split. **What v3 adds:** an explicitly-named routing key (D2) that is the structural answer to points 2 and 4 above; a queryable durable edge (D3); a scoped — not deleted — visibility filter (D6); an ancestor-chain ownership walk (D7) that is strictly safer than *both* options v2 offered; and one interrupt entry point with an explicit scope (D8).
>
> **v2 changelog (retained).** v1 framed this as "parent/child session parity" and was returned REVISE by two independent reviews. v2 corrected three factually wrong v1 premises: a durable parent→child edge already exists; children's conversations are fully persisted (not ephemeral); and a childID-keyed cascade already exists for hard-abort.

---

## 1. Context

Omnipus has **two mechanisms for running an agent**, and only one of them is a special case.

**The normal path — own session.** Task and plan execution mint a real session and stamp their own transcript id:

- `pkg/agent/task_executor.go:528`, `:1958` — `sessStore.NewSession(session.SessionTypeTask, "system", t.AgentID)`
- `pkg/agent/task_executor.go:1252`, `pkg/agent/plan_engine.go:2787` — `TranscriptSessionID: taskSessionID` (its **own** id)
- `pkg/agent/plan_engine.go:3965` — `mintPlanSession` → `store.NewSession(...)`, same shape

**The special case — delegate sub-turn.** A delegated child does neither:

- `pkg/agent/subturn.go:1034` — `TranscriptSessionID: parentTS.transcriptSessionID` (inherits the parent's id — "FR-6a")
- `pkg/agent/subturn.go:1020` — `SessionKey: childID` (its **own** id)
- `pkg/tools/delegate.go:1248` — *"UnifiedStore.NewSession is never called for a child turn"*

A delegated child therefore carries **two identity namespaces**: a unique `delegateSessionID` (`== childID == sessionKey`, minted at `pkg/tools/delegate.go:1105`) for per-delegation operations, and the parent's `transcriptSessionID` for subtree grouping and transcript writes. Confusing the two is a silent no-op. That is not hypothetical — it is the confirmed root cause of two P0 bugs from the 2026-07-31 live UAT: **#576** (`message_parent` read the wrong key, failed 100% of the time) and **#577** (`delegate cancel` resolved the wrong key, returned success while cancelling nothing). Both are fixed; the shape that produced them remains. The codebase names the hazard explicitly:

```go
// pkg/tools/delegate.go:556-561
// The two pairs share the same Go signature
// (func(string, string) ([]string, error)) so the compiler cannot catch a
// re-wire: a future maintainer "fixing consistency" by switching back to
// InterruptSession/InterruptSessionHard would silently reintroduce the
// dual-namespace bug where every delegate.cancel no-op'd against
// transcriptSessionID while still reporting success.
```

### 1.1 The real subject of this ADR

`transcriptSessionID` is not one thing. It plays **three distinct roles** that FR-6a fused into one string:

| Role | What it answers | Consumers |
|---|---|---|
| **A — Own identity** | "which session directory do I write to; which store record am I" | transcript writes, media uploads, session owner, tool manifest, audit attribution, `repairHistory` |
| **B — Subtree grouping** | "which live turns belong to this chat's tree" | cancel cascade + escalation, pre-arm latch, ADR-045 watchdog, `turn_canceled` audit descendants |
| **C — Client routing** | "which SPA bucket does this WS frame belong in" | every session-scoped WS frame payload |

v2 proposed deleting the fusion and said nothing about roles B and C, which is why five shipped mechanisms break silently. **v3 splits the roles explicitly**: role A becomes the session's own id (D1), role B and role C become one explicitly-named field with exactly two consumer classes (D2), and the durable edge that outlives the in-memory map becomes queryable (D3).

The honest summary: **this ADR does not delete FR-6a's inheritance. It narrows it from a ~30-consumer identity key to a 2-consumer routing key with a name that says what it is.**

---

## 2. Decision

**Route the delegate sub-turn onto the existing own-session path**, and split the three roles above so that each has exactly one field, one meaning and an enumerable consumer set.

### D1 — The child's own id is its session id, its `sessionKey`, and its `transcriptSessionID`

`UnifiedStore.NewSession` mints its own id (`pkg/session/unified.go:405-418`, id at `:410`) and cannot be used as-is. The exact-id primitive already exists and is already used by another caller:

```go
// pkg/session/unified.go:439-440
// createSessionLocked creates a session directory with the EXACT supplied id,
// meta.json, and an empty transcript. Caller must hold us.mu.
```
```go
// pkg/session/unified.go:582  (GetOrCreateScheduledSession — the existing exact-id caller)
return us.createSessionLocked(id, SessionTypeScheduled, "scheduled", ownerAgentID)
```

`createSessionLocked` is **unexported**; a sibling exported wrapper is added (W1). Two things it does **not** do today and must do for the child:

1. **It does not set `meta.Owner`** (`pkg/session/unified.go:447-461` constructs `UnifiedMeta` with ID/AgentID/AgentIDs/ActiveAgentID/Status/Channel/CreatedAt/UpdatedAt only). The child's meta must inherit the parent's `Owner`, or `WithSessionOwner` silently stops installing (`pkg/agent/loop.go:6844-6848` guards on `meta.Owner != ""`) and **SEC-2/#406 rule 2 becomes a no-op with zero signal** inside every delegated child.
2. **`SessionMeta` has no parent field** (verified: zero `parent`/`Parent` matches across the whole struct, `pkg/session/daypartition.go:76-185`) and `UnifiedSessionType` has no subordinate value (`pkg/session/unified.go:26-57`: `chat`, `task`, `channel`, `scheduled`, `heartbeat`, `verifier`). Both are added (W2).

**Consequence:** `steer`/`respond`/`cancel`/`peek`/`inbox`/`follow_up` keep taking exactly the id they take today, because `delegateSessionID == sessionKey` is preserved (`pkg/agent/subturn.go:693-697` documents this alignment as the reason no id-mapping table exists).

### D2 — `routingSessionID`: the one field that stays inherited, with a name and a closed consumer set

A new `turnState` field, `routingSessionID`, is **inherited verbatim** from the parent (for a root turn it equals the turn's own session id, so all root behaviour is byte-identical). It is a distinct named type (`RoutingSessionID`, W20) so the compiler separates it from `SessionID`.

It has **exactly two consumer classes, and this list is the contract**:

**(a) Client routing.** Every session-scoped WS frame's `session_id` is stamped from `routingSessionID`. This is load-bearing: the SPA buckets **strictly** by the frame's own `session_id`, with no chat check —

```ts
// src/store/chat.ts:2883-2885   (handleFrame)
const targetSid: string | null = (() => {
  if (frame.type === 'session_started') return activeSid
  if (frameSessionId) return frameSessionId      // ← the bucket key
```

`matchesEvent` (`pkg/gateway/websocket.go:3007-3018`) only decides whether the gateway *writes* the frame to the socket; it does not decide where the browser *files* it. `tool_call_start` and `tool_call_result` sit in `SESSION_SCOPED_FRAME_TYPES` alongside `subagent_start`/`subagent_end` (`src/store/chat.ts:1236-1249` — **19** types, not 20). Without D2, `ToolExec*` payloads (`pkg/agent/loop.go:8688`, `:9038`) would carry the child id while `SubTurn*` payloads stay parent-scoped (`pkg/agent/subturn.go:1183`, `:1424`), and the span and its steps land in two different buckets. The correlation chain is `session_id → bucket` (`chat.ts:2885`) → `parent_call_id → span` (`chat.ts:3593`, `:3605`, `:3813`, `:3964`) → `call_id → step` (`chat.ts:3829`); the first hop breaks and nothing downstream runs. The out-of-order buffer splits identically — its key is `` `${targetSid}:${parentCallId}` `` (`chat.ts:3620`, `:3876`, `:3940`) with a 10 s TTL (`chat.ts:1189`), so a `tool_call_start` arriving before its `subagent_start` is buffered under one key and looked up under another, then flushed flat. The only signal is `logDiagnostic('chatAttachStepSpanIndexMiss', …)` (`chat.ts:1959`) — not user-visible.

**(b) In-memory subtree predicates.** These four ask "which live turns belong to this chat's tree", and all four re-base onto `routingSessionID` with **no change in cost** (still one `activeTurnStates.Range`):

| Predicate | Site | Today's match |
|---|---|---|
| `collectDescendantTurnIDs` | `pkg/agent/steering.go:429` | `ts.transcriptSessionID == sessionID` |
| `InterruptSession` / `InterruptSessionHard` | `steering.go:459`, `:519` | same |
| `sessionTurnsStillAlive` | `steering.go:745` | `ts.transcriptSessionID == sessionID && ts.IsAlive()` |
| `hasLiveCriticalDelegate` | `steering.go:787` (+ `:790` root-skip, `:793` `ts.critical`) | `ts.transcriptSessionID != sessionID` → skip |

Plus the three `turn.go` resolvers that share the same predicate: `GetActiveTurnHookForSession` (`turn.go:524`), `resolveSessionIDByChannelChat` (`turn.go:564`, returning at `:581`), `getActiveRootTurnStateForSession` (`turn.go:607`).

**And the pre-arm latch, which D2 fixes structurally with no extra work.** `preArmKeysForTurn` keys on the turn's identity (`cancel_prearm.go:355`), a Stop arms under `"s:"+sessionID` (`:338`), and the pending-spawn marker is set and cleared under the **parent's** identity (`subturn.go:585`, `:1147`). The design's stated correctness argument is:

```go
// pkg/agent/cancel_prearm.go:385-389
//     the same parent identity the child inherits verbatim (opts.Channel/
//     opts.ChatID/TranscriptSessionID all copy straight from parentTS, see
//     spawnSubTurn's processOptions construction), so the SAME keys the
//     marker was set under are the ones cleared, with no recomputation
//     needed from the child's own (freshly-constructed) fields.
```

D2 preserves "inherits verbatim" **precisely** — the pre-arm keys move from `transcriptSessionID` to `routingSessionID`, which is the same value, inherited the same way. The invariant the comment relies on survives intact.

**`routingSessionID` is explicitly NOT**: a session-store key, a transcript write target, an ownership predicate, a steering-queue scope, an approval-grant key, an uploads-directory key, a tool-manifest bucket, a lifecycle-record field, or an audit `session_id`. This exclusion list is enforced by test (§10 AC-2).

### D3 — The durable parent→child edge is `ParentDurableKey`, and it must be made queryable

**`OwnerScopeID` is unusable and v2 was wrong to name it.** The mint site:

```go
// pkg/tools/delegate.go:1117-1122
ownerScopeKind := session.OwnerScopeHuman
ownerScopeID := ""
if parentDelegateID := strings.TrimSpace(ToolDelegateSessionID(ctx)); parentDelegateID != "" {
    ownerScopeKind = session.OwnerScopeParentSession
    ownerScopeID = parentDelegateID
}
```

`WithDelegateSessionID` has **exactly one non-test call site** — `pkg/agent/subturn.go:1080`, on a *child* turn's context. A root chat turn's ctx carries no delegate session id, so for **every direct child of a chat turn** `ownerScopeID == ""`. Three places state it as contract: `pkg/session/lifecycle.go:141-143` (*"OwnerScopeID is empty"*), `lifecycle.go:229` (*"OwnerScopeID is `""` for a top-level delegation"*), and the mint site's own comment at `delegate.go:1130-1131`. A walk over `OwnerScopeID` would reach depth 2 and deeper while missing depth 1 entirely — the inverse of a useful failure mode.

**`ParentAgentID` cannot stand in.** It is an *agent config id* (`ToolAgentID(ctx)`, stamped at `delegate.go:1149`, stored at `:1172`), documented as *"the ONLY parent linkage… the sole legal predicate for 'the subagents I started'"* (`lifecycle.go:208-233`). Two chats where the same agent delegates are indistinguishable.

**`ParentDurableKey` is the edge.** It is stamped from `ToolTranscriptSessionID(ctx)` at `run` time (`delegate.go:1106`) and is **always populated regardless of `OwnerScopeKind`** (`lifecycle.go:235-244`). Today it is a subtree-shared routing key; **under D1 it becomes a genuine strict-direct-parent session edge at every depth**, because the parent's `transcriptSessionID` is now the parent's own id. This is exactly the property D7 exploits, and v2 used it in its D5 while missing it in its D2.

Three things must change for the walk to exist at all:

1. **`LifecycleFilter` cannot express it.** It has exactly five fields — `WorkspaceID`, `AgentID`, `ParentAgentID`, `States`, `NonTerminalOnly` (`pkg/session/lifecycle.go:543-563`) — and `matches` **explicitly refuses** to match on `ParentDurableKey` (`:572-575`). A `ParentDurableKey` field and match clause are added (W6).
2. **`List` has no index.** `scanSessionIDs()` is an `os.ReadDir` over the whole lifecycle directory, then `s.Load(id)` opens and parses every line of every file (`lifecycle.go:617-636`). A transitive walk would be one full-directory scan plus a full file parse **per depth level**. A secondary parent index is added (W6) so "children of X" is one file read; the walk is then O(descendants), not O(all sessions ever).
3. **Three doc comments become false and must be rewritten in the same change** (`lifecycle.go:225-228`, `:572-575`, `pkg/tools/list_jobs_sources.go:311-315`). All three justify refusing to infer parentage from `ParentDurableKey` on the grounds that it is *"SHARED between a parent and its children and every cousin in the subtree"*. That premise dies with FR-6a. Leaving them makes the next maintainer re-derive a conclusion that is no longer true. **This is a hard requirement of W6, not a tidy-up.**

**`list_jobs` is unaffected** — it filters on `ParentAgentID`, a different axis (`list_jobs_sources.go:324`, re-checked in-process at `:430`), and all three shipped kinds scope by agent id, not session id (`:176`, `:255`, `:324`).

### D4 — Cancel: in-memory subtree off `routingSessionID`; the durable walk is for non-turn resources only

`RequestCancel` has a 3 s graceful → hard window and a 5 s → detach window. A per-hop unindexed store scan inside that window is not viable. The split:

**Live turns — in-memory, unchanged cost.** PHASE A computes the live subtree **once** from `routingSessionID` and threads the set through PHASE B and PHASE C, which today each re-scan:

```go
// pkg/agent/cancel.go:462   (PHASE B, 3s → hard)
if len(al.sessionTurnsStillAlive(sessionID)) == 0 { return }
// pkg/agent/cancel.go:487   (PHASE C, 5s → detach)
stillAlive := al.sessionTurnsStillAlive(sessionID)
if len(stillAlive) == 0 { return }
```

Without D2 both predicates see only the root, conclude "already finished", and return — so the child is **never hard-aborted or detached**. `sessionTurnsStillAlive`'s doc comment documents the exact bug it exists to close, and that fix depends entirely on the child sharing the id:

```go
// pkg/agent/steering.go:730-733
// an un-hard-aborted child simply retries with a fresh, uncanceled context
// and keeps running — invisibly, for as long as its own task takes (minutes,
// for a multi-step delegate) — until it resurfaces, sometimes concurrently
// with a later, unrelated delegate call on the same session.
```

The **ADR-045 watchdog interlock** is the same shape. `hasLiveCriticalDelegate` is condition 2 of the three-part fire predicate (`orphan_watch.go:327-330`, `:336-338`, `:341-343`); without D2 it returns `false` unconditionally and the watchdog reaps the root while a `Critical: true` async delegate (`delegate.go:1402` — *every* async delegation) runs alongside it. Its own comment says why it exists:

```go
// pkg/agent/orphan_watch.go:280-287
//     RequestCancel's PHASE B/C escalation
//     (InterruptSessionHard/sessionTurnsStillAlive) is session-wide by
//     construction and cannot be scoped to "the root only" from the outside
//     — so rather than reap anyway and rely on PHASE A's mere graceful nudge
//     … this mechanism defers reaping ENTIRELY while the delegate survives.
```

And `collectDescendantTurnIDs` (`steering.go:425-435`) feeds the `turn_canceled` **audit** entry — consumed at `cancel.go:337`, emitted as `"descendants_canceled": descendants` at `cancel.go:376`. Without D2 that list silently empties and the audit trail stops recording what a Stop reached (a repudiation issue, not just cosmetics).

**Non-turn resources — one durable walk per Stop, off the escalation path.** Run once, on its own goroutine, over the D3 index:

- **Background shells.** `ProcessSession.OwnerSessionID` is stamped from `ToolTranscriptSessionID(ctx)` (`pkg/tools/shell.go:571-572` → `:1035`) and `KillAllForSession` matches on it (`pkg/tools/session.go:455`); `pkg/tools/session.go:95-103` calls it *"the chat/transcript session ID that owns this background process."* `RequestCancel` fires `hooks.KillBackgroundSessions(sessionID)` **unconditionally and before `ClaimCancel`** (`cancel.go:233-234`, vs `ClaimCancel` at `:284`), so today one Stop reaps every descendant's detached processes in one call. Under D1 it reaps none. The stamp moves to the child's own id and the kill cascades over the descendant set. **Additionally**: `InterruptBySessionKey` — the `delegate cancel` path — **never calls `KillBackgroundSessions` at all** (verified: the only non-test call site in the tree is `cancel.go:234`), so a per-delegation cancel already leaks shells today, and after D1 nothing else would reap them. `delegate action=cancel` must now kill that child's background shells.
- **Pending approvals.** See D5.
- **Lifecycle transitions.** `RequestCancel` transitions one id and tolerates not-found (`cancel.go:428`). The walk must transition **each descendant's** record to `cancelled`.
- **Per-session state teardown.** See D5(c).

**Coverage of the durable record — five populations, examined individually.** `LifecycleRecord{` has exactly **two** construction sites in the non-test, non-generated tree (`pkg/tools/delegate.go:1166`, `pkg/agent/task_executor.go:228`). Taking each gap in turn:

| # | Gap | Verdict |
|---|---|---|
| 1 | Plan owner/supervision sessions mint no `LifecycleRecord` (`plan_engine.go:3951-3975` calls `NewSession` only) | **Not a D3 hole.** The walk finds children of X by `ParentDurableKey == X`; the *parent* needs no record of its own. A plan-owned session that delegates has children whose records carry its id. But it does mean the lifecycle store is **not** a complete session graph — a claim this ADR does not make. Plan cancel stays the explicit list-walk (D9). |
| 2 | Task-dispatch records leave `ParentAgentID` **and** `ParentDurableKey` empty and put a **plan id** in `OwnerScopeID` (`task_executor.go:202-208`, `:224-233`) | **Not a D3 hole, and it is why `OwnerScopeID` is doubly unusable** — a walk over it would mistake a plan id for a session id. A task dispatch has no delegating parent by construction; it is reachable as a walk *root*, not as a child. |
| 3 | The mint is **skipped entirely** when `t.lifecycle == nil` (`delegate.go:1123` guards the whole block; wired conditionally at `session_messaging_wire.go:141-143`) | **A real hole, and now fatal.** With D3 the record is the *only* cancel edge, so a nil store means a Stop cancels nothing with no error. **Delegation is refused without a lifecycle store** (W7) — the same fail-closed posture the mint site already takes for an unresolvable parent agent (`delegate.go:1150-1157`). |
| 4 | `tools.delegate.require_parent_agent_id=false` mints with a blank `ParentAgentID` (`delegate.go:1149-1164`) | **Not a D3 hole.** The kill switch blanks `ParentAgentID`; `ParentDurableKey` is stamped unconditionally at `:1106` and `:1173`. It does degrade `list_jobs` attribution — which the code already announces on **every** such mint (`slog.Error` at `:1159`). |
| 5 | External-CLI (3P) children's own sub-delegations never reach the mint | **A real hole, and out of the session graph by construction** — they run inside a foreign CLI's process tree, outside the Omnipus tool surface. The boundary is the 3P child's own process kill; W9's acceptance criterion asserts the child's process group dies, so the subtree dies with it. |

### D5 — A delegated child inherits its parent's standing tool approvals, re-keyed, and is torn down

v2 took no position. The code today:

```go
// pkg/security/approvalgrants.go:112, :119, :123
func (s *ApprovalGrantStore) Inherit(sessionID, parentAgentID, childAgentID string) {
    parentSet, ok := s.grants[grantKey{sessionID: sessionID, agentID: parentAgentID}]
    ...
    childKey := grantKey{sessionID: sessionID, agentID: childAgentID}
```

- **Write, at spawn:** `al.ApprovalGrants().Inherit(parentTS.transcriptSessionID, parentTS.agentID, agent.ID)` — `pkg/agent/subturn.go:916`.
- **Read, inside the child's turn:** `al.ApprovalGrants().IsAllowed(ts.transcriptSessionID, ts.agentID, toolName)` — `pkg/agent/loop.go:8617`; and `CheckGrantOrRequestApproval(turnCtx, ts.transcriptSessionID, …)` at `:8630-8631`.

Under D1 without a decision, `Inherit` writes under `<chatSid>` and the child reads under `<childID>`. **Every inherited grant misses** (map miss → `return false`, `approvalgrants.go:66-68`). The failure direction is safe (re-prompt, never auto-approve), but the availability impact is severe: the child falls through to `CheckGrantOrRequestApproval` and blocks on a human for up to **300 s per tool call** (`pkg/gateway/approvals.go:150`, armed at `:241`) — and delegate spans are hidden from the thread unless verbose chat is on (`src/lib/toolVisibility.ts:218-223`: `shouldRenderSubagentSpan` returns `verboseChatEnabled` and nothing else). The observable symptom is **a delegation that hangs for five minutes with no prompt and no explanation.**

**Decision: (a) inherit, re-keyed to the child.** `Inherit`'s first argument becomes the child's own session id. Rationale: the human approved a tool *for this conversation*, and a delegation is that conversation's work. The security consequence, stated plainly: **a grant now visibly crosses a session boundary.** It already crosses that boundary today — invisibly, via the shared key. Making it explicit, scoped to `{childSessionID, childAgentID}`, and terminating with the child session is a net improvement over an implicit share with no lifetime.

**(b) Pending-approval teardown re-scoped.** `ToolApprovalRequest.SessionID` is stamped from `ts.transcriptSessionID` (`loop.go:8473`) and the registry filters on it (`approvals.go:419`). The cancel hook `cancelAllPendingForSession(sid, reason)` (`websocket.go:1978` → `approvals.go:414`) is called with whatever `RequestCancel` resolved. It must run over D4's descendant set, or a chat-level Stop leaves a child's approvals pending with their timers running and the child's goroutine blocked on `resultCh`.

**(c) Child sessions get a `CloseSession`.** `CloseSession` clears grants (`session_end.go:45`) and evicts the loaded-tool manifest via `forgetSession` (`:37`). Its only call sites are `websocket.go:1038` (explicit WS close), `loop.go:1048`/`:1064` (idle sweep), and `session_end.go:865` (boot sweep) — **none on a child/delegate path**. Without this, per-child grant sets, `loadedTools` buckets, `metaCache` entries and `recallSpans` accumulate for the process lifetime. Note `forgetSession`'s `recallSpans` cleanup uses `key == sessionID || strings.HasSuffix(key, ":session:"+sessionID)` (`loop.go:11497-11500`); a child's `sessionKey` is a bare UUID, so the `key == sessionID` arm matches — the heuristic survives D1 provided something actually calls it.

### D6 — Scope the transcript visibility filter; do not delete it

v2 deleted `IsDelegateChildEntry()` and its filter sites on the reasoning that *"child content is no longer in the parent's file, so there is nothing to filter."* That is true only of **future** transcripts. The predicate is content-based, not location-based:

```go
// pkg/session/daypartition.go:332-334
func (e TranscriptEntry) IsDelegateChildEntry() bool {
	return e.ParentSpawnCallID != ""
}
```

Every existing `transcript.jsonl` already contains child narration and final reports. Deleting the filter dumps them into every historical chat as top-level bubbles — verbatim the regression the filter was written to fix (`pkg/gateway/rest.go:814-822`). And **leaving** the filter unchanged makes `GET /api/v1/sessions/{childID}` and `inspect_session(childID)` return an empty transcript, killing the drill-down that is this ADR's headline gain.

**Decision: keep the predicate, scope the filter.** Hide a delegate-child entry only when the requested session is **not** the session that produced it — i.e. filter ON for a chat/task/plan session, OFF for a delegate child's own session. The discriminator is available: `SessionMeta.ParentSessionID`/`SessionType` from W2, or the lifecycle record's existence.

**Consequence: no migration is required.** Historical transcripts hide exactly what they hide today; the child's own drill-down shows everything; new transcripts are unaffected because the child's entries are no longer in the parent's file at all.

**There are four filter sites (v2 named two), and one helper serves two REST handlers — five effective read boundaries:**

| Site | Surface | Rule after D6 |
|---|---|---|
| `pkg/gateway/replay.go:298` | live-reconnect replay of a chat | filter ON |
| `pkg/gateway/rest.go:826` (`filterDelegateChildEntries`, `:823-832`) | `getSession` **and** `getSessionMessages` cold-load | scoped |
| `pkg/agent/verifier_adjudication.go:406` (`renderTranscriptEntriesForWindow`) | **what the verifier/Judge sees** — deleting it changes adjudication inputs | scoped |
| `pkg/tools/inspect_session.go:172` | the agent-facing `inspect_session` tool | scoped |

**A finding neither v2 nor the review caught: the filter is already leaky today.** Only three writers stamp `ParentSpawnCallID` onto a `TranscriptEntry` — `turn.go:1204` (intermediate), `turn.go:1268` (final), `websocket.go:4254` (streamed). `appendToolCallTranscript` (entry literal `turn.go:1123-1129`) and `appendErrorTranscript` (entry literal `turn.go:1314-1324`) do **not**, so a delegate child's tool-call entries and error entries are visible in the parent chat right now — including external-CLI tool calls, which route through `appendToolCallTranscript` (`external_dispatch.go:550-555`). D1 removes them from the parent's file as a side effect. This is a **gain**, and it means the "un-hiding" C-5 warns about is already partially real today.

**An unclaimed gain this ADR should take credit for (m-4).** `HydrateAgentHistoryFromTranscript` reads the transcript **without** the filter (`pkg/agent/attach_hydrate.go:34-42`; zero `IsDelegateChildEntry` references in that file) and runs on every reload (`websocket.go:2577`) and on the self-heal path (`loop.go:6204`). Because a self/untargeted delegation runs with `execSource.ID` equal to the parent's own agent id (`subturn.go:731`, `:776-779`, `:841-842`), **the delegate's raw narration is currently absorbed into the parent agent's own LLM context on reload.** D1 fixes this. It is also a behaviour change to the parent's context that reviewers must see coming.

### D7 — Ownership gate: walk the ancestor chain (strictly safer than either v2 option)

`callerOwnerKey(ctx)` returns `ToolTranscriptSessionID(ctx)` (`delegate.go:1966-1968`) and is compared for equality against `rec.ParentDurableKey` (`delegate.go:1973-1979`). Because every descendant currently inherits the root chat's transcript id, this gate is **chat-subtree-wide today**: a parent can address its grandchildren, **and a child can address its siblings and cousins.** It gates exactly six actions at six call sites: `inbox` (`:2010`), `steer` (`:2107`), `respond` (`:2159`), `cancel` (`:2321`), `follow_up` (`:2459`), `peek` (`:2592`).

v2 offered two options: strict direct-parent (loses root-over-subtree control) or "walk the `OwnerScopeID` chain" (impossible — D3). **v3 chooses a third that is better than both: walk the `ParentDurableKey` chain upward** from `rec` toward the caller, bounded by the configured max delegation depth (`delegationDepthResolver`, `delegate.go:1096-1098`). Each hop is one `Load`; depth is bounded; D3's index is not even needed for the upward direction because each record names its own parent.

Net effect versus today:
- **Preserved:** root-over-subtree control (a chat can `cancel`/`steer` a grandchild).
- **Removed:** the sibling/cousin leak — a sibling is not an ancestor. This is a genuine security improvement that neither v2 option delivered.
- **Cost:** O(depth) bounded `Load`s per gated action, off any latency-critical path.

> **§9 Q1 keeps the fail-closed alternative** (strict direct-parent) as a genuine operator posture choice. It is no longer a cost trade-off — both are affordable — so it is purely "should a chat retain reach into its grandchildren".

### D8 — One interrupt entry point with an explicit scope enum

Today two functions have **identical Go signatures** and differ only in cascade semantics:

```go
// pkg/agent/steering.go:449
func (al *AgentLoop) InterruptSession(sessionID, hint string) (descendants []string, err error)
// pkg/agent/steering.go:611
func (al *AgentLoop) InterruptBySessionKey(sessionKey, hint string) (descendants []string, err error)
```

Today they are distinguishable because they take ids from *different namespaces*. After D1 they take the same id, and only the scope differs — recreating the confusion class the ADR claims to eliminate, on the cancel path, in the code #577 just fixed (`delegate.go:556-561` already flags the hazard by name).

**Decision: collapse to one entry point with a mandatory explicit scope** — `Interrupt(id SessionID, scope InterruptScope, hint string)` where `InterruptScope ∈ {ScopeSubtree, ScopeSelfOnly}` (plus the `Hard` variants). The compiler forces every caller to name its intent; no two same-shaped functions remain to be confused.

**Reconciling with #577, explicitly.** #577's fix (`steering.go:590-594`) reads:

```go
// This function is intentionally scoped to ONE turn: cascading via the
// shared transcriptSessionID (i.e. reusing InterruptSession as-is) would also
// interrupt the delegating parent's own live turn and any sibling
// delegations sharing that chat session — far broader than what a
// per-delegation cancel is supposed to do.
```

The *intent* is "cancel one delegation without touching the parent or siblings". The *implementation* had to be a second function **because the shared transcript id made any cascade unusable**. After D1 that constraint is gone: `ScopeSubtree` rooted at a child reaches exactly that child's own descendants — never the parent, never a sibling. So D1 does not regress #577; **it makes #577's intent expressible as a scope rather than as a workaround.**

This also fixes a live leak: `delegate action=cancel` today cancels one turn and leaves that child's **own** grandchildren running. Under D8 it becomes `ScopeSubtree` rooted at the child, which is what it should always have done. That is a behaviour change and is called out as such (R-13).

`pkg/agent/interrupt_by_session_key_test.go:9-19,232` pins the two-namespace split and must be **deliberately inverted**, not deleted, asserting the new invariant (§10 AC-8).

### D9 — Plan cancel is unchanged, and it bounds this ADR's scope

`StopPlan` (`plan_engine.go:2044-2135`) already builds an explicit `[]string` of member + verifier + owner + supervision session ids under `planDecisionMu` and calls `RequestCancelForSession` once per id (`:2330-2385`). It is already an explicit list-walk, not a cascade — the same shape D4 adopts. **No change to plan cancel.**

Likewise this ADR unifies the **execution primitive**, not the orchestration. Plan-owned tasks keep their deterministic engine trigger, dependency DAG, judge-round adjudication, and the ADR-053 D7 rule that members have no individual start/cancel. Delegate-owned tasks keep their parent-free-will trigger and per-delegation control. **One primitive, two trigger policies.** The consequence of D9 is scope: it is what makes the D3 gap analysis (rows 1 and 2) acceptable rather than a hole.

---

## 3. Complete enumeration of the affected surface

The single highest-value input the review named, and it is one command:

```
rg -n "transcriptSessionID" --glob '!*_test.go' pkg/     # 116 refs, 18 files
rg -n "ToolTranscriptSessionID\(" --glob '!*_test.go' pkg/   # 19 call sites
```

Every read is classified below. **`FIXED` and `UNAFFECTED` rows are evidence-backed rebuttals of findings that assumed a break.**

### 3.1 Role A — own identity (redirected to the child's own session)

| Consumer | Site(s) | Classification |
|---|---|---|
| Transcript writes ×4 | `turn.go:1130`, `:1208`, `:1270`, `:1325` | **SILENT** → W3. Writes land in the child's file; `AppendTranscript` returns `nil` even if that file's session does not exist |
| Streamed transcript write | `websocket.go:4254` | **SILENT** → W3 |
| External-CLI writes | `external_dispatch.go:463`, `:550-555`, `:562-564` | **SILENT** → W3 |
| Approval transcript mutate | `approval_transcript.go:179`, `:183` | **SILENT** → W3 |
| Session owner (`WithSessionOwner`) | `loop.go:6844-6848` | **SILENT** → W1 must copy `meta.Owner`; otherwise SEC-2/#406 rule 2 is a no-op |
| Tool-media uploads | `normalization.go:247-254` → `media/tempdir.go:33-51` | **SILENT (disk leak)** → W18. `CleanupPolicyForgetOnly` is immune to the TTL cleaner; today parent and children share one dir |
| Loaded-tool manifest | `tool_manifest.go:20-25` (prefers `transcriptID`) | **BEHAVIOUR CHANGE** → each child starts with an empty progressive-disclosure set (token/latency cost per delegation) and needs teardown (D5c) |
| Async result persistence | `loop.go:8776` → `async_notifier.go:50` → `loop.go:6546-6559` | **BEHAVIOUR CHANGE (improvement)** — persists into the child's own session once W1 makes it resolvable via `ResolveSessionStore` (`loop.go:5012-5039`) |
| `repairHistory` | `loop.go:7389-7390` → `repair.go:79-86` | **FIXED** — today the child repairs against the parent's mixed file, so a synthetic tool_use can be drawn from the parent's calls; after D1 it repairs against its own |
| `sessionActiveAgent` resolver | `loop.go:6653-6656` | **FIXED** — `hand_off` is structurally excluded from a child registry (`subturn.go:988` → `registry.go:667-669`), so a child can never hand off; the resolver correctly returns `""` and the turn's starting agent (the delegate target) is stamped. Today the parent's post-handoff agent id can be stamped on a child's entries |
| External-CLI consent + in-place mutate | `external_dispatch.go:380`, `:609`, `:646` | **UNAFFECTED** — write and mutate both use `childTS.transcriptSessionID`, so they stay consistent before and after |
| `recall_conversation` | `recall_conversation.go:158-161` | **UNAFFECTED** — prefers `ToolSessionKey(ctx)`, which is already the child's own id |
| `hand_off`'s `resolveSessionID` | `handoff.go:255-260` | **UNAFFECTED** — unreachable in a child (see above) |
| Audit attribution | `list_jobs.go:541`, `path_audit.go:161`/`:208`, `memory.go:180`/`:527`, `plan_correct.go:335`, `sysagent/tools/task.go:1095`, `loop.go:2379` | **BEHAVIOUR CHANGE (improvement)** — the audit `session_id` now names the session that actually performed the action. Any query that grouped by chat must join via `ParentDurableKey`. **[INFERRED]** that no such query exists today; no aggregation consumer was found |

### 3.2 Role B — subtree grouping (moves to `routingSessionID`, D2)

| Consumer | Site(s) | Without D2 |
|---|---|---|
| `collectDescendantTurnIDs` → `turn_canceled` audit | `steering.go:429` → `cancel.go:337`, `:376` | **SILENT** — descendant list empties; audit stops recording what a Stop reached |
| `InterruptSession` / `InterruptSessionHard` | `steering.go:459`, `:519` | **SILENT** — cascade reaches the root only |
| `sessionTurnsStillAlive` (PHASE B/C gate) | `steering.go:745` ← `cancel.go:462`, `:487` | **SILENT** — escalation returns early; child never hard-aborted or detached |
| `hasLiveCriticalDelegate` (ADR-045 interlock) | `steering.go:787-793` ← `orphan_watch.go:336` | **SILENT** — returns `false` forever; watchdog reaps live `Critical:true` background work |
| `GetActiveTurnHookForSession` H1 root-preference | `turn.go:519-543` | **PRESERVED by D2** — multiple turns still share `routingSessionID`, so H1 stays live (it would be dead code without D2) |
| `resolveSessionIDByChannelChat` (Tier-B `/stop`) | `turn.go:557-583` | **SILENT** — must return `routingSessionID`; otherwise a channel `/stop` with only a surviving child alive resolves the **child's** id and cancels only the child |
| `getActiveRootTurnStateForSession` (ADR-045) | `turn.go:603-617` | must match on `routingSessionID` to keep its root-exclusive contract |
| Cancel pre-arm latch | `cancel_prearm.go:338`, `:355`, `:602`; `subturn.go:585`, `:1147` | **RESOLVED STRUCTURALLY by D2** — "inherits verbatim" (`cancel_prearm.go:385-389`) stays literally true |
| Background shells | `shell.go:571` → `session.go:455` ← `cancel.go:233-234` | **SILENT** → D4/W9 |
| Approval grants | `subturn.go:916` ↔ `loop.go:8617`, `:8630-8631` | **SILENT** → D5 |
| Pending approvals | `loop.go:8473` → `approvals.go:414-419` ← `websocket.go:1978` | **SILENT** → D5b |

### 3.3 Role C — client routing (moves to `routingSessionID`, D2 + W5)

| Payload | Site | Note |
|---|---|---|
| `TurnEndPayload.SessionID` | `loop.go:6991` | stamp from `routingSessionID` |
| `ToolExecStartPayload.SessionID` | `loop.go:8688` | the C-2 case |
| `ToolExecEndPayload.SessionID` | `loop.go:9038` | the C-2 case |
| `bus.OutboundMediaMessage.SessionID` | `loop.go:8920` | `media` is session-scoped in the SPA |
| Streamer resolution | `loop.go:7577` (`al.bus.GetStreamer(ctx, ts.channel, ts.chatID, ts.transcriptSessionID)`) | routing, not identity |
| `SubTurnSpawnPayload` / `SubTurnEndPayload` `.SessionID` | `subturn.go:1183`, `:1424` (child id rides as `Label`, `:1177`) | already `parentTS`-sourced; **pin to `routingSessionID` with a regression test** (W21) |
| `ToolApprovalRequest.SessionID` | `loop.go:8473` | **not** a WS payload — this is the approval registry key; see D5b |

**The contract rule (W5).** `session_id` on every session-scoped frame means the **routing key** — the chat/root session the client files it under — and is stamped from `routingSessionID`. A new optional `producing_session_id` carries the session that produced the event and is present iff it differs. All **19** `SESSION_SCOPED_FRAME_TYPES` are audited against this rule; the child drill-down view filters on `producing_session_id`.

Two pre-existing strains this exposes but does not cause (stated so a reviewer does not attribute them here): `RateLimitPayload` has **no** `SessionID` field at all (`pkg/agent/events.go:525-533`) and its `session_id` is reconstructed from the connection's chat→session map (`websocket.go:3461` → `sessionIDForChat`, `:3022`), so a reconstructed `""` is dropped in production; and `'replay_done'` is in `SESSION_SCOPED_FRAME_TYPES` but absent from the `WsFrameType` enum on both sides.

---

## 4. Consequences

### Gained

- One execution mechanism instead of two; the sub-turn special case is deleted, not extended.
- The dual-namespace silent-no-op class (#576/#577) is eliminated by construction, and D8 removes the one place it would otherwise have reappeared.
- **UI drill-down into a child session** with the existing components (`src/routes/_app/sessions.$sessionId.tsx` → `<ChatScreen />`), because `GET /api/v1/sessions/{childID}` resolves — today it 404s (`rest.go:834-844`, no `UnifiedMeta`).
- **Unblocks ADR-056's cut `shell` kind (#564)**, which was cut precisely because *"a background shell carries no agent id, and a delegated child shares its parent's transcript session, so a shell row cannot be attributed to a principal at all"* (`pkg/tools/list_jobs.go:25-30`).
- **The parent agent's LLM context stops absorbing delegate narration on reload** (D6, m-4).
- **Delegate tool-call and error entries stop appearing as top-level bubbles in the parent chat** (D6 — they never carried `ParentSpawnCallID`, so the filter never caught them).
- **The sibling/cousin ownership leak is closed** (D7).
- `delegate action=cancel` stops leaking that child's own grandchildren and its background shells (D8, D4).

### Lost / changed

- **This is not a net deletion.** Four deletions (FR-6a inheritance, `NoHistory: true`, the dual namespace, `InterruptBySessionKey` as a separate function) against ~20 additions across §6. Call it what it is: **a simplification of the identity model that costs a large migration.**
- Each child starts with an empty loaded-tool manifest (token + latency cost per delegation).
- `follow_up` warm resume reuses `childID` **verbatim** for the next generation (`subturn.go:1115-1135`). With `NoHistory: false` and a real session behind that id, generation N+1 loads generation N's history. **Decision: that is intended** — a corrective follow-up should see what it is correcting. Stated here because "we deleted a flag" is not a specification of resume semantics (R-11, AC-11).
- Audit `session_id` becomes the acting session, not the chat.

---

## 5. Risk register

Each risk names its failure *shape*, because in this migration almost every failure is success-shaped.

| ID | Risk | Shape | Addressed by | AC |
|---|---|---|---|---|
| R-1 | `delegateStatusExtra` → `recentActivityLines(task.SessionID, …)` reads the parent's transcript, finds nothing, returns nil (`delegate.go:1823`, documented silent-nil `:1844-1851`) | silent | W14 | AC-9 |
| R-2 | Background shells orphaned by a chat Stop; `delegate cancel` never reaped them at all | silent | D4, W9 | AC-6 |
| R-3 | Span and steps land in different SPA buckets on the **first delegation, live connection** | silent (one dev-only diagnostic) | D2, W5 | AC-3 |
| R-4 | Cancel escalation ladder + audit descendants + ADR-045 interlock stop working | silent | D2, D4 | AC-4, AC-5 |
| R-5 | Approval grants miss; pending approvals never cancelled; 300 s invisible block | silent | D5 | AC-7 |
| R-6 | Pre-arm latch filed under one key, consumed under another | silent (`notifyLatchExpired` reports after the fact) | D2 | AC-4 |
| R-7 | `AppendTranscript` `MkdirAll`s an orphan dir and returns `nil`; `ReadTranscript` returns `[]` + `nil` (`unified.go:814-823`, `:1194-1196`) | **silent, and it is why a green suite proves nothing** | W3 | AC-1 |
| R-8 | Session-store contention: `UnifiedStore.mu` is one non-striped `RWMutex` (`unified.go:161`); `NewSession` holds the **write** lock across `MkdirAll` + two `WriteFileAtomic` cycles, each with a file `Sync()` **and a parent-directory `Sync()`** (`fileutil/file.go:97`, `:121`); `AppendTranscript` (`:810-811`, meta rewrite `:848`) and `ListSessions` (`:1248`, also the write lock) contend on the same mutex. A 24-way fan-out serialises 24 fsync-bound creates **and stalls token streaming in every other session in the store** | latency regression, not a directory count | W15, W17 | AC-10 |
| R-9 | Session listing/UI: no parent field on `SessionMeta`, no subordinate `SessionType`, **no pagination at any layer** (`unified.go:1247`, `loop.go:5046`, `rest.go:758-812`, `src/lib/api.ts:1379-1388`), sidebar `maxVisible = 9` by recency (`Sidebar.tsx:456`) → 24 child sessions evict the parent chat; `SearchModal` renders unvirtualized (`SearchModal.tsx:687`); `RetentionSweep` walks the whole tree (`retention_sweep.go:35`) | visible but unbounded | W2, W16 | AC-10 |
| R-10 | `WithSessionOwner` silently not installed → SEC-2/#406 rule 2 unenforced | silent | D1, W1 | AC-2 |
| R-11 | `follow_up` warm resume now sees the previous generation's history | behaviour change | §4 (accepted) | AC-11 |
| R-12 | Tool-media uploads land in a per-child dir immune to the TTL cleaner and session cascade-delete | silent disk leak | W18 | AC-12 |
| R-13 | `delegate action=cancel` changes from one-turn to that child's subtree | behaviour change (fixes a leak) | D8 | AC-8 |
| R-14 | `ParentDurableKey`'s meaning inverts; three doc comments and one filter axis become false | doc rot → future re-derivation of a wrong rule | W6 | AC-13 |
| R-15 | The ActivityPanel is **not** a usable fallback for hidden delegations: `subagent_message`/`subagent_state` have **zero Go emitters** (repo-wide grep over `*.go` returns 0), are absent from the `WsFrameType` enum in contracts, Go and TS, and their structs are dead declarations (`asyncapi_types.gen.go:496`, `:521`). `AgentActivityItem.lifecycleState`/`sessionMessages`/`steeringReceipt` are therefore permanently `undefined` (`useRunningActivity.ts:495-510`), and `SubagentSpanTerminal.finalResult` (`chat.ts:100`) is never populated | pre-existing; this ADR must not lean on it | W19 | AC-14 |

---

## 6. Work items

Ordered; W3 and W20 come first because everything else is verified against them.

| # | Item | Why |
|---|---|---|
| **W3** | **`AppendTranscriptStrict`** — fail loudly on a session id with no `meta.json`; convert the four `turn.go` writers, `websocket.go:4254`, `external_dispatch.go` and `approval_transcript.go`. Note the existing warning at `turn.go:1300-1311` fires on `store == nil \|\| sessionID == ""`, **not** on an unresolvable id, and the `ts.abandoned` suppression at `:1296-1299` is entirely silent | R-7. Do this **before** anything else, or every later acceptance criterion is measured against a primitive that reports success for a lost write |
| **W20** | **Named ID types** (`SessionID`, `RoutingSessionID`) — promoted from v2's "companion" to a **precondition** | M-3. After D1 the type system is what keeps role A and role B apart |
| **W1** | Exact-id session-create wrapper over `createSessionLocked`; call from `spawnSubTurn` with `childID`; `opts.TranscriptSessionID = childID`; mint into the **same** shared `*session.UnifiedStore` the delegate tool holds (`loop.go:1727-1728`); **copy the parent's `meta.Owner`**; delete `NoHistory: true` (`subturn.go:1032`) | D1, R-10 |
| **W2** | `SessionMeta.ParentSessionID` + a subordinate `UnifiedSessionType` + the OpenAPI enum + SPA. Precedent: `verifier` needed a store enum, an OpenAPI enum and an SPA change (`rest.go:783-785`) | D6 discriminator, R-9 |
| **W4** | `turnState.routingSessionID` + inheritance + the closed consumer set; re-base the seven role-B predicates and the pre-arm keys | D2 |
| **W5** | WS contract: `session_id` = routing key; add `producing_session_id`; audit all 19 `SESSION_SCOPED_FRAME_TYPES`. Constraint #8's 5-step pipeline (`contracts/` → `scripts/gen-contracts.sh` → commit generated diff → handler) | D2(a), R-3 |
| **W6** | `LifecycleFilter.ParentDurableKey` + `matches` clause + a parent secondary index maintained inside `Persist` under the existing 64-shard striped lock (`lifecycle_lock.go:19-31`, precedent `message_inbox.go:135-139`); **rewrite `lifecycle.go:225-228`, `:572-575`, `list_jobs_sources.go:311-315`** | D3, R-14 |
| **W7** | Delegation is refused when no lifecycle store is wired (`session_messaging_wire.go:141-143` currently makes it optional), mirroring `delegate.go:1150-1157`'s fail-closed posture | D3 gap 3 |
| **W8** | `RequestCancel`: compute the live subtree once in PHASE A and thread it through B/C; run the durable walk once, on its own goroutine; transition **each** descendant's lifecycle record (`cancel.go:428` transitions one) | D4, R-4 |
| **W9** | `ProcessSession.OwnerSessionID` from the child's own id; cascade `KillBackgroundSessions` over the descendant set; **`delegate action=cancel` kills that child's shells**; assert a 3P child's process group dies with it | D4, R-2, D3 gap 5 |
| **W10** | Approval grants re-keyed to the child (`subturn.go:916`); `cancelAllPendingForSession` over the descendant set; child `CloseSession` on child-turn terminal | D5, R-5 |
| **W11** | Scope the visibility filter at all four sites | D6 |
| **W12** | `verifyCallerOwnsSession` ancestor-chain walk, depth-bounded; all six call sites | D7 |
| **W13** | Collapse `InterruptSession`/`InterruptBySessionKey` (+ `Hard`) into one entry point with an explicit `InterruptScope` | D8, R-13 |
| **W14** | `recentActivityLines` uses the delegate session id and **logs** its empty path. Also: `executeSync` registers a `DelegateTaskState` (only `executeAsync` does today — `delegate.go:1315`, inside `executeAsync` at `:1280`; `executeSync` at `:1507` registers nothing, so `status`'s activity snapshot is *already* absent for every synchronous delegation), and `t.tasks`/`t.sessionIndex` get a deletion path (**no `delete(t.tasks` or `delete(t.sessionIndex` exists anywhere** — both grow for the process lifetime) | R-1, M-13 |
| **W15** | Stripe `UnifiedStore.mu`, or move the two fsync-and-rename cycles out of the write lock | R-8 |
| **W16** | Pagination on `GET /api/v1/sessions` through all four layers; sidebar filter for subordinate sessions | R-9 |
| **W17** | **Root-level delegation admission gate.** `AdmissionController` gates inbound user-message dispatch only and says so: *"Subagent spawn and task-executor dispatch paths are NOT gated"* (`admission.go:12-18`), and `spawnSubTurn`'s bypass is documented at `cancel_prearm.go:778-780`. `turnState.concurrencySem` is set **only** on a child (`subturn.go:1051`), so nested delegation is gated and **root-level delegation is not** — matching the live "24 parallel against a cap of 16" observation in `docs/internal/uat/max-parallel-concurrency-gap-2026-07-31.md` §G1 | Required **by this ADR**: D1 turns every delegation into an fsync-bound session create behind R-8's global lock, so an ungated root fan-out becomes a self-inflicted DoS |
| **W18** | Child uploads directory reachable by session cascade-delete (`normalization.go:247-254` → `media/tempdir.go:33-51`) | R-12 |
| **W19** | Drill-down surface (`GET /api/v1/sessions/{childID}` → `<ChatScreen />`) as **the** stated inspection surface for hidden delegations, replacing any reliance on the ActivityPanel | R-15 |
| **W21** | Pin `SubTurnSpawnPayload.SessionID`/`SubTurnEndPayload.SessionID` to `routingSessionID` with a regression test; re-point `DelegateTaskState.SessionID` (`delegate.go:1303`) deliberately | R-3 |
| **W22** | Deliberately invert — never quietly delete — the gate tests that encode the current contract: `subturn_test.go:2095` (`TestSubTurnInheritsTranscriptSessionID`, asserting equality at `:2143-2145`), `approval_grant_delegation_test.go:19,229`, `cancel_orphan_delegate_test.go:57-79`, `cancel_subagent_cascade_test.go:51-101`, `cancel_session_isolation_test.go:12`, `orphan_watch_test.go:14,223-229`, `steering_test.go:1693,1765-1811,1865`, `interrupt_by_session_key_test.go:9-19,232`, `subturn_transcript_nesting_test.go:9-10,93-94`, `cancel_async_delegate_repro_test.go`, `gateway/cancel_subagent_cascade_test.go:5`, `gateway/replay_test.go:1549`. Roughly 71 test files and ~430 references touch this value (128 `transcriptSessionID` refs across 43 test files alone) | The suite is the specification of the current contract |

### Removed from scope (not deferred — never validated as belonging here)

**Throttle unification.** v2's item 9 proposed unifying `turnState.concurrencySem` (block-with-timeout), `TaskExecutor.dispatchSema` and `TaskExecutor.maxConcurrent` (try-and-refuse), and reconciling their back-pressure semantics. Nothing in D1–D9 touches concurrency semantics, and folding it in would mean a regression in either mechanism is attributed to the other during bisection — in a migration whose own §10 says silent failure is the expected mode. v2's premise was also overstated: *"the semaphore is never set on a root turn"* is true, but `childTS.concurrencySem = make(chan struct{}, rtCfg.maxConcurrent)` (`subturn.go:1051`) means **nested** delegation *is* gated. **This ADR takes no position on unifying the three throttles.** What it does take a position on is the one piece it makes load-bearing — the ungated **root-level** fan-out — which is W17, in scope, with its own acceptance criterion. v2's §8 Q4 ("block-with-timeout vs try-and-refuse") is removed with the rest.

---

## 7. Alternatives considered

**A. Named ID types only** (`type SessionKey string` / `type TranscriptSessionID string`). Captures the #576/#577 type-confusion win cheaply, but keeps both mechanisms: none of §2's deletions, does not unblock #564, does not enable drill-down, and does not fix the ADR-045/pre-arm/grant/escalation coupling. **Rejected as the endpoint; promoted from "companion" to precondition (W20).**

**B. Keep the split, patch bugs as found.** Rejected: the 2026-07-31 fix wave already patched five instances of this shape; the shape is the defect generator.

**C. Full parity including per-child transcript retention semantics** (v1's R6). **Resolved, not deferred** — D6 answers it: the filter is scoped, not deleted, so historical retention semantics are unchanged and the child's own retention follows the ordinary session policy.

**D. Mint the child's `UnifiedMeta` lazily on first drill-down**, so R-8's fan-out cost is paid only when someone looks. **Rejected.** It reintroduces exactly the failure R-7 describes: between spawn and first drill-down the child writes into a directory with no meta, which is invisible to `ListSessions`, to `replay.go` and to `GET /api/v1/sessions/{id}` (404 via `ResolveSessionStore`'s `GetMeta` probe, `rest.go:834-844` ← `loop.go:5012-5039`), while every write returns `nil`. It also makes AC-1 unassertable. The correct answer to the cost is W15 + W17, not a lazier record.

**E. Keep FR-6a and add the child's own id as a second field** (the inverse of D1). Rejected: it preserves the dual namespace and adds a third id. D2 already keeps the one genuinely useful part of FR-6a under a name that says what it is.

---

## 8. Relationship to ADR-053

ADR-053 D1 was amended **2026-07-31** to state FR-6a is *retained and load-bearing*. That amendment was a truth-in-documentation fix: D1 originally claimed "FR-6a dropped", which contradicted the code and would have re-broken the chat-wide Stop if implemented in good faith. This ADR **deliberately supersedes that amendment** by changing the system rather than the description — D1's "isolated-but-linked" intent becomes literally true in code.

v2 named only D1. Every ADR-053 decision whose *implementation* rides `ParentDurableKey` or the shared transcript id, with its new value:

| ADR-053 | Implementation | New value under ADR-057 |
|---|---|---|
| **D1** (dual namespace ratified) | `subturn.go:1034` | **Superseded.** One id (D1); the routing role is renamed and narrowed (D2) |
| **D5** (ownership gate) | `verifyCallerOwnsSession`, `delegate.go:1973-1979`, six call sites | **Changed.** Equality → depth-bounded ancestor walk (D7). Sibling/cousin reach removed; root-over-subtree preserved |
| **D15** (per-child message ceiling) | keyed on the same owner key | **Changed.** The ceiling becomes per-direct-parent instead of per-chat-subtree, so a chat's total is now (children × ceiling) rather than one shared pool. **Stated deliberately**; AC-15 pins it |
| **D16** (ad-hoc inboxes keyed to the durable chat/plan id; *"Inbox survives a parent Stop/Play"*) | `ownerKeyFor(rec) = rec.ParentDurableKey` (`message_parent.go:327-331`); producer `Append(ownerKey)` (`:640` ← `:407`); consumers `Drain(ownerKey, …)` (`delegate.go:2024`) and `Drain(rec.ParentDurableKey, …)` (`:2200`) | **Changed, and v2 changed it silently.** `ParentDurableKey` becomes the **immediate** parent's id, so a grandchild's `message_parent` output routes to its direct parent's inbox rather than the chat's. That is arguably more correct — a child reports to whoever delegated to it — but D16's stated property changes and the producer/consumer pair must move together or `delegate action=inbox` returns a clean, empty success payload forever. AC-16 pins producer↔consumer agreement at depth 3 |

**Sequencing.** ADR-053's epic is **not closed**: the 2026-07-31 UAT produced 12 defects (#576–#588) whose fixes are still landing on this branch (`0ee87fbe`, `b120c8c2`, `ac7d6d74` are all delegate/`message_parent` fixes from the last few commits). **Decision: this ADR lands after the #576–#588 wave closes.** Rationale: W22 inverts a dozen gate tests in exactly the files that wave is still editing, and R-4/R-5's failures are the same silent shape those defects were; overlapping the two makes attribution impossible. This is a hard prerequisite, recorded here rather than in §9, because it is not an open question.

---

## 9. Open questions (operator decisions only)

Two. Everything else the review raised is decided above.

1. **D7 posture.** Ancestor-chain walk (recommended: preserves root-over-subtree, removes the sibling leak) or strict direct-parent (fail-closed: a chat can no longer reach its grandchildren directly, only through the intermediate child). Both are affordable; this is purely a security-posture choice.
2. **R-9 listing policy.** Are subordinate sessions hidden by default with an opt-in flag (the `verifier` precedent, `rest.go:783-785` + `?include_verifier=true`), or shown nested under their parent? W2 supplies the data either way; the SPA treatment differs.

---

## 10. Verification requirements (non-negotiable)

**The governing fact.** Almost every failure in this migration is *success-shaped*: a predicate returns "nothing to do" and every caller proceeds happily. This project's precedent is `plan_engine.go:3937-3944` — a derived `plan:<id>` id that cancelled nothing in production for months while every test passed, because the fake canceller recorded the string it was handed and returned success.

**Two consequences.**

1. **Every criterion below is verified against real store-backed state and real registered turns. A spy or mock that records its argument and returns success is disallowed, without exception.**
2. **AC-1 comes first and gates the rest.** Until `AppendTranscript` fails loudly, a green suite is not evidence: today it `MkdirAll`s the directory, writes the line, fails `readMetaLocked`, logs `slog.Warn("unified_store: could not update meta stats")` and **returns `nil`** (`pkg/session/unified.go:814-823`); `ReadTranscript` on a missing path returns `[]TranscriptEntry{}, nil` (`:1194-1196`). It is a silent **create**, not a silent drop — so an assertion of the form "the append succeeded" can never fail.

| AC | Risk | Criterion |
|---|---|---|
| **AC-1** | R-7 | `AppendTranscript` against a UUID with no `meta.json` returns a non-nil error and creates **no** directory. Each of the four `turn.go` writers plus `websocket.go:4254` surfaces that error (counter + WARN). Then: after one delegation, `<store>/<childID>/meta.json` exists and `GET /api/v1/sessions/{childID}` returns 200 with non-empty messages |
| **AC-2** | R-10, D2 | A test enumerates every read of `routingSessionID` in the non-test tree and fails if it appears outside the closed consumer set (WS payload stamping + the seven role-B predicates + pre-arm keys). Separately: after one delegation, `system.workspace.create` inside the child stamps a non-empty owner equal to the parent's (`WithSessionOwner` installed, `loop.go:6844-6848`) |
| **AC-3** | R-3 | **Client-side bucket membership on the LIVE connection** — not frame delivery, and not after a reconnect. Drive one delegation through the real gateway; assert the SPA store's `<chatSid>` bucket contains the span **and** its steps, `spanByParentCallId` resolves, and `logDiagnostic('chatAttachStepSpanIndexMiss')` never fires. Repeat with a reconnect as a second case. A `producing_session_id` round-trip test covers all 19 session-scoped frame types |
| **AC-4** | R-4, R-6 | A real registered root that finishes gracefully + a real registered `Critical:true` child that does not + a real Stop → assert PHASE B hard-abort **and** PHASE C detach both fire against the child, and the `turn_canceled` audit entry's `descendants_canceled` (`cancel.go:376`) is non-empty and names the child. Separately, the pre-arm race (`cancel_async_delegate_repro_test.go`): a Stop arriving before the child registers is consumed by the child, not expired |
| **AC-5** | R-4 | A live `Critical:true` async delegate + an orphaned root → the ADR-045 watchdog does **not** fire (`hasLiveCriticalDelegate` returns true through `routingSessionID`), and does fire once the delegate finishes |
| **AC-6** | R-2 | A child starts a background `bash`; a chat-level Stop kills it (real PID gone). A `delegate action=cancel` on that child also kills it. A sibling's background shell survives both |
| **AC-7** | R-5 | With a standing grant on the parent, a delegated child executes the granted tool with **no** approval prompt and no 300 s wait. With a pending approval inside a child, a chat-level Stop cancels it (registry entry gone, timer stopped, the child's goroutine unblocks). After the child terminates, its grant set, `loadedTools` bucket and `recallSpans` entries are gone |
| **AC-8** | R-13 | `Interrupt(childB, ScopeSubtree)` cancels B and B's own children, and leaves parent A and sibling C running (the inverted `interrupt_by_session_key_test.go` assertion). `Interrupt(chat, ScopeSubtree)` reaches all three depths |
| **AC-9** | R-1 | `delegate action=status` returns a non-empty activity snapshot for a **sync** delegation (today `executeSync` registers no `DelegateTaskState` at all) and for an async one; the empty path logs |
| **AC-10** | R-8, R-9 | **A concurrency scenario, explicitly.** A 24-way root fan-out while a second session streams tokens: assert the second session's inter-token latency stays within a stated budget, and that W17's gate refuses the 25th rather than queueing it behind the store lock. Assert `GET /api/v1/sessions` paginates and the sidebar still shows the parent chat |
| **AC-11** | R-11 | `follow_up` on a completed child resumes with generation N's history visible in generation N+1's first assembled message list |
| **AC-12** | R-12 | Deleting a parent session removes `<home>/uploads/<childID>/` for every descendant |
| **AC-13** | R-14 | A doc-truth test (or review gate) asserting that `lifecycle.go:225-228`, `:572-575` and `list_jobs_sources.go:311-315` no longer describe `ParentDurableKey` as shared parent↔child |
| **AC-14** | R-15 | The drill-down surface is reachable and populated for a hidden delegation **without** verbose chat enabled, using only `GET /api/v1/sessions/{childID}`. No criterion depends on `subagent_message`/`subagent_state`, which have no emitter |
| **AC-15** | ADR-053 D15 | The per-child message ceiling is enforced per direct parent at depth 3, and a chat's aggregate is (children × ceiling) — asserted, not assumed |
| **AC-16** | ADR-053 D16 | At depth 3, `message_parent` from the grandchild is drained by its **direct parent's** `delegate action=inbox` and by nobody else; producer (`message_parent.go:640`) and consumer (`delegate.go:2024`, `:2200`) agree |
| **AC-17** | D3 gaps | Negative paths: (a) delegate with the lifecycle store unwired → the delegation is **refused** with an operator-visible error, never a silent skip (W7); (b) delegate with `require_parent_agent_id=false` → the child is still reachable by the `ParentDurableKey` walk and a Stop cancels it; (c) a 3P child's own subprocess tree dies with the child's process group |
| **AC-18** | D6 | On a session recorded **before** the cutover, a cold load and a live replay both still hide delegate narration (no regression to `rest.go:814-822`'s described failure). On a child's own session, `inspect_session` and `GET /api/v1/sessions/{childID}` return the full transcript. The verifier's adjudication window (`verifier_adjudication.go:406`) is byte-identical for a pre-cutover parent session |
| **AC-19** | migration | A session **in flight** across a deploy: the parent's turn is mid-delegation when the process restarts. Assert the boot sweep reconciles the child's lifecycle record and no transcript write lands in an orphan directory |

**m-5's warning applies to the whole suite:** `pkg/agent/message_parent_real_context_test.go:16-17` already notes its fixture *"happens to make `ToolTranscriptSessionID`"* equal the seeded id — i.e. an existing test would **not** catch a divergence introduced here. Every criterion above must construct the parent and child ids as *distinct values* and assert on which one was used.

---

## Appendix — disposition of every review finding

`corrected` = the decision changed · `scoped` = added as explicit work with an AC · `rebutted` = evidence shows the finding does not hold as stated. **Nothing is deferred.**

| # | Finding | Disposition | Where |
|---|---|---|---|
| C-1 | `OwnerScopeID` empty for generation 1 | **corrected** — edge is `ParentDurableKey`; `OwnerScopeID` named unusable, with three code citations | D3 |
| C-2 | R-3 breaks on the live connection; `session_id` conflates routing + producing | **corrected + scoped** — D2 + a stated contract rule over all 19 frame types | D2(a), §3.3, W5, AC-3 |
| C-3 | Approval grants + pending approvals + no child cleanup | **corrected (position stated) + scoped** | D5, W10, AC-7 |
| C-4 | Escalation ladder + ADR-045 interlock fail silently | **corrected** — D2 restores both in-memory at unchanged cost | D2(b), D4, AC-4, AC-5 |
| C-5 | Un-hides historical transcripts; four sites not two | **corrected** — filter scoped not deleted, so no migration; four sites tabled; plus a new finding that the filter is already leaky | D6, W11, AC-18 |
| M-1 | Shell scope bigger than stated; `delegate cancel` never reaped | **scoped** | D4, W9, AC-6 |
| M-2 | Pre-arm latch invariant deleted | **corrected structurally** — D2 keeps "inherits verbatim" literally true | D2, §3.2, AC-4 |
| M-3 | D1 collapses the #577 distinction | **corrected + reconciled** — one entry point with an explicit scope; #577's intent becomes *expressible* rather than regressed | D8, W13, W20, AC-8 |
| M-4 | Walk not expressible; O(all sessions) per hop in a 3 s window | **corrected** — filter field + parent index; and the hot path uses in-memory `routingSessionID`, so the durable walk leaves the escalation window entirely | D3, D4, W6, W8 |
| M-5 | Five durable-record coverage holes | **2 scoped, 3 rebutted** — gaps 3 and 5 are real (W7, W9); gaps 1, 2, 4 do not break a `ParentDurableKey` walk, with reasons | D3 table |
| M-6 | `AppendTranscript` silently succeeds into an orphan dir | **scoped, first** — and made the gate for every other AC | W3, AC-1 |
| M-7 | Six further silent consumers | **3 scoped, 3 rebutted** — owner stamping, uploads and manifest are real (W1, W18, W10); `sessionActiveAgent` and `repairHistory` are *fixed* by D1, external-CLI consent is unaffected — each with evidence | §3.1 |
| M-8 | R-5 is a global-lock contention regression | **corrected + scoped** — reclassified as R-8/R-9 with a measured budget; Alternative D evaluated and rejected on R-7 grounds | R-8, R-9, W15, W16, Alt D, AC-10 |
| M-9 | §7 changes D16 silently; epic still open | **corrected + scoped** — D1/D5/D15/D16 each given a new value; sequencing decided (after #576–#588) | §8, AC-15, AC-16 |
| M-10 | §3 is a sample, not coverage; bus steer path already broken | **corrected** — §3 rebuilt from the full `rg` enumeration; the bus path (`session_messaging_wire.go:476-478` builds `"agent:<id>:<sid>"` while a child's `sessionKey` is a bare UUID, `subturn.go:1020`) is confirmed broken **today** and named | §3, R-list note |
| M-11 | `NoHistory` deletion + manifest re-bucketing asserted without analysis | **corrected** — `follow_up` resume semantics stated as intended; manifest cost and teardown named | §4 Lost, D5c, AC-11 |
| M-12 | Item 9 is unrelated scope | **cut, explicitly** — throttle unification removed (never validated as belonging here; the ADR takes no position). The one piece this ADR *does* make load-bearing — the ungated root fan-out — is retained as W17 with its own AC | §6 "Removed from scope", W17, AC-10 |
| M-13 | R-1's surrounding state is in-memory-only and never freed; `executeSync` registers nothing | **scoped** | W14, AC-9 |
| M-14 | ActivityPanel fallback is non-functional | **corrected** — the ADR stops leaning on it; drill-down becomes the stated inspection surface; the dead-frame evidence is recorded | R-15, W19, AC-14 |
| m-1 | "Net deletion" does not survive §5 | **corrected** — claim withdrawn in the changelog and §4 | changelog ¶3, §4 |
| m-2 | D5 is an open question inside the Decision section | **corrected** — D7 is now a decision with a recommendation; only the posture choice remains in §9 | D7, §9 Q1 |
| m-3 | §9's bar covers three risks | **corrected** — 19 acceptance criteria across 15 risks, with AC-1 as the gate | §10 |
| m-4 | Unclaimed benefit (hydration absorbs delegate narration) | **adopted** | D6, §4 Gained |
| m-5 | Citation accuracy is the floor; an existing fixture would not catch a divergence | **adopted** — a distinct-ids requirement now applies to the whole suite; and one wrong v2 citation is corrected in the changelog | §10 closing note, changelog ¶6 |
