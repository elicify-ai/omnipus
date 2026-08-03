# Feature Specification: ADR-057 — Unify delegate sub-turns onto the own-session execution path

**Created**: 2026-08-03
**Status**: Draft
**Input**: [ADR-057 v4](../architecture/ADR-057-session-parent-child-parity.md) (Accepted, commit `7d7def6f`), shaped by [ADR-057 review](../architecture/ADR-057-session-parent-child-parity-review.md) (adversarial red-team, verdict BLOCK on v2). This spec is the implementation layer for ADR-057 v4's decisions D1–D12 and work items W1–W24. **ADR-057's acceptance criteria AC-1 … AC-22 are carried forward verbatim in §"Acceptance Criteria (verbatim from ADR-057 v4 §10)" and are non-negotiable.**

**Branch**: `feature/plan-swimlane-board`. **Greenfield** — no migration, no back-compat, for chats or config files (ADR-057 v4 operator decision 1).

---

## The governing constraint: silent failure

Read this before anything else in this document.

> Almost every failure in this migration is **success-shaped**: a predicate returns "nothing to do" and every caller proceeds happily.

The canonical mechanism, verified 2026-08-03 against the live tree:

```go
// pkg/session/unified.go:819-823   (inside AppendTranscript)
meta, err := us.readMetaLocked(sessionID)
if err != nil {
    slog.Warn("unified_store: could not update meta stats", "session_id", sessionID, "error", err)
    return nil
}
```

The line before it (`pkg/session/unified.go:814`) calls `fileutil.AppendJSONL`, which begins with `os.MkdirAll(dir, 0o700)` (`pkg/fileutil/file.go:207-210`). So an append against a session id that **does not exist** creates the directory, writes the line, fails the meta read, logs a WARN, and **returns `nil`**. Its read counterpart is symmetric: `ReadTranscript` returns `[]TranscriptEntry{}, nil` on `os.IsNotExist` (`pkg/session/unified.go:1192-1194`).

This is a silent **create**, not a silent drop. An assertion of the form *"the append succeeded"* can therefore never fail, which is why a green test suite currently proves almost nothing about this migration.

The project has been burned by exactly this shape before, and the code says so:

```go
// pkg/agent/plan_engine.go:3937-3944
// Both MUST be REAL, store-backed sessions. A derived or composed id
// ("plan:<id>") is forbidden and is the defect this replaces: nothing in the
// tree ever CREATED that session, so processSystemMessage's transcript
// resolution (which resolves by GetMeta against a real store) dropped it, the
// turn ran with an empty transcriptSessionID, and RequestCancelForSession —
// which matches on exactly that value — found nothing to cancel. Every test
// of that cascade passed anyway, because the fake canceller records the string
// it was handed and returns success.
```

**Three rules bind every test in this spec, without exception:**

1. **Every acceptance criterion is verified against REAL store-backed state and a REAL registered turn.** A spy, fake, or mock that records the argument it was handed and returns success is **disallowed**. Where a test needs a store, it gets a real `UnifiedStore` rooted at a `t.TempDir()`. Where it needs a turn, it gets a turn registered in `activeTurnStates`.
2. **Assertions land on observable artefacts, not on invocation.** Files on disk and their bytes; process IDs that are gone; registry entries that no longer resolve; SPA store buckets. Never "the flush function was called".
3. **Cross-process and store-level guarantees copy the shape of `pkg/entity/store_crossprocess_test.go`** — which re-execs the test binary as real OS processes (`//go:build !windows`, verified present). Performance properties assert a **slope** (doubling concurrency must not double wall-clock), never a machine-specific constant.

**Corollary — distinct ids everywhere.** `pkg/agent/message_parent_real_context_test.go:16-17` already records that its fixture *"happens to make `ToolTranscriptSessionID`"* equal the seeded id, i.e. an existing test would **not** catch a divergence introduced here. Every test written for this spec MUST construct parent and child ids as distinct, non-equal values and assert on **which one** was used.

---

## Citation corrections (verified 2026-08-03, `feature/plan-swimlane-board`)

ADR-057 v4 demands citation accuracy as the floor (finding m-5). Every ADR citation this spec depends on was re-opened. Three drifted or were under-specified; the corrections below are what this spec uses. **No ADR *decision* changes as a result — these are pointer fixes.**

| ADR-057 says | Verified actual | Impact |
|---|---|---|
| `unified.go:1194-1196` — `ReadTranscript` silent-empty | `pkg/session/unified.go:1192-1194` (`if os.IsNotExist(err) { return []TranscriptEntry{}, nil }`) | none — same construct, off by 2 |
| `websocket.go:4254` — "streamed transcript write" | `:4254` is the `ParentSpawnCallID: parentSpawnCallID,` stamp; the `AppendTranscript` call is `pkg/gateway/websocket.go:4256` | W3 must convert `:4256`; W11's provenance retention concerns `:4254` |
| `session_messaging_wire.go:141-143`, `normalization.go:247-254`, `media/tempdir.go:33-51` (no package prefix) | `pkg/agent/session_messaging_wire.go:141-143` (NOT `pkg/gateway/`), `pkg/tools/normalization.go:247-254`, `pkg/media/tempdir.go:33-51` — all three line ranges exact | file-ownership assignment only |

Everything else this spec cites was re-verified exact, including: `pkg/session/unified.go:161` (single `sync.RWMutex`), `:405-418`/`:410`, `:415-416`, `:439-440`, `:448-460` (the `UnifiedMeta` literal — **no `Owner` field**), `:463`, `:466`, `:472`, `:582`, `:586`, `:614`, `:764`, `:786`, `:810-811`, `:819-823`, `:824-847`, `:848`, `:1247`, `:1388`, `:1397`, `:1494`, `:182`, `:192`; `pkg/fileutil/file.go:97`/`:121` (file and parent-directory `Sync()`); `pkg/session/lifecycle_lock.go:17`/`:29-31`/`:35-39`; `pkg/session/message_inbox.go:139`; `pkg/entity/lock.go:12`; `pkg/session/lifecycle.go:543-563` (exactly five filter fields) and `:571-575` (`matches` refusing `ParentDurableKey`); `pkg/session/daypartition.go:209-223` (`SessionStats`, **9** fields) and **9** `Goal*` + **9** `Loop*` fields in `SessionMeta`, `:332-334` (`IsDelegateChildEntry`); the four filter sites `pkg/gateway/replay.go:298`, `pkg/gateway/rest.go:826` (helper `:823-832`), `pkg/agent/verifier_adjudication.go:406`, `pkg/tools/inspect_session.go:172`; `pkg/agent/subturn.go:916`, `:1020`, `:1032`, `:1034`, `:1051`; `pkg/tools/delegate.go:1105`, `:1106`, `:1117-1122`, `:1123`, `:1966-1968`, `:1973-1979`; `pkg/agent/turn.go:1130`/`:1208`/`:1270`/`:1325`; `pkg/agent/loop.go:6844-6848`; `pkg/agent/cancel.go:233-234`, `:462`, `:487`; `pkg/agent/steering.go:425`/`:449`/`:511`/`:611`/`:665`/`:738`/`:780`; `pkg/agent/admission.go:12-18`; `pkg/security/approvalgrants.go:112-123`; `src/store/chat.ts:1236-1249` (**19** `SESSION_SCOPED_FRAME_TYPES`, counted) and `:2883-2885`. All twelve `*_test.go` files named by W22 exist.

**`producing_session_id` (W5) is genuinely new**: `rg -c producing_session_id contracts/ src/ pkg/` returns zero matches tree-wide.

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Site | Role in this change |
|---|---|---|
| `UnifiedStore.AppendTranscript` | `pkg/session/unified.go:802` | **modified** — gains a strict sibling (W3); the counter path becomes in-memory (W24) |
| `UnifiedStore.createSessionLocked` | `pkg/session/unified.go:441` | **extended** — exported exact-id wrapper (W1); must copy parent `Owner` |
| `UnifiedStore.mu` | `pkg/session/unified.go:161` | **replaced** — 64-shard `sessionLock` + narrow `cacheMu` (W15) |
| `UnifiedStore.writeMetaLocked` | `pkg/session/unified.go:786` | **replaced** — four targeted writers (W23) |
| `UnifiedStore.readMetaLocked` / `readUnifiedMeta` | `:764` / `:1494` | **extended** — compose four files (W23) |
| `UnifiedStore.ListSessions` | `pkg/session/unified.go:1247` | **modified** — per-session reconcile, `cacheMu.RLock` snapshot (W15); paginated (W16) |
| `UnifiedStore.Close` | `pkg/session/unified.go:1388` | **extended** — gains a flush hook that does not exist today (W24) |
| `SessionMeta` | `pkg/session/daypartition.go:76-185` | **extended + split** — `ParentSessionID` (W2); persistence splits four ways (W23) |
| `SessionStats` | `pkg/session/daypartition.go:209-223` | **relocated** — becomes `stats.json` (W23) |
| `TranscriptEntry.IsDelegateChildEntry` | `pkg/session/daypartition.go:332-334` | **deleted** (W11) |
| `LifecycleFilter` / `matches` | `pkg/session/lifecycle.go:543-563` / `:565+` | **extended** — `ParentDurableKey` field + clause + parent index (W6) |
| `lifecycleStripedLock` | `pkg/session/lifecycle_lock.go:17-39` | **pattern source** — copied verbatim for `sessionLock` (W15) |
| `turnState.transcriptSessionID` | `pkg/agent/turn.go:225` | **split** — role A stays; roles B/C move to `routingSessionID` (W4) |
| `spawnSubTurn` | `pkg/agent/subturn.go` | **modified** — mints a real session, drops `NoHistory` (W1) |
| `AgentLoop.InterruptSession` / `InterruptBySessionKey` (+`Hard`) | `pkg/agent/steering.go:449`/`:611`/`:511`/`:665` | **collapsed** into one scoped entry point (W13) |
| `collectDescendantTurnIDs`, `sessionTurnsStillAlive`, `hasLiveCriticalDelegate` | `pkg/agent/steering.go:425`/`:738`/`:780` | **re-based** onto `routingSessionID` (W4) |
| `RequestCancel` | `pkg/agent/cancel.go` | **modified** — subtree computed once, durable walk added (W8) |
| `ApprovalGrantStore.Inherit` | `pkg/security/approvalgrants.go:112` | **re-keyed** to the child session (W10) |
| `verifyCallerOwnsSession` / `callerOwnerKey` | `pkg/tools/delegate.go:1973-1979` / `:1966-1968` | **replaced** — ancestor-chain walk (W12) |
| `AdmissionController` | `pkg/agent/admission.go:12-18` | **extended** — gates root-level delegation (W17) |
| `SessionUploadsDir` | `pkg/media/tempdir.go:33-51` | **cascade** — child dirs reachable by parent delete (W18) |
| `SESSION_SCOPED_FRAME_TYPES` | `src/store/chat.ts:1236-1249` | **audited** — all 19 types against the routing rule (W5) |
| `handleFrame` bucketing | `src/store/chat.ts:2883-2885` | **contract anchor** — the bucket key that D2 exists to protect |

### Impact Assessment

Blast radius measured by the ADR's own enumeration command (`rg -n "transcriptSessionID" --glob '!*_test.go' pkg/` → 116 refs / 18 files; `ToolTranscriptSessionID(` → 19 call sites), plus ~430 references across ~71 test files.

| Symbol modified | Risk | d=1 dependents | d=2 dependents |
|---|---|---|---|
| `turnState.transcriptSessionID` | **CRITICAL** | 4 transcript writers, 7 subtree predicates, 6 WS payload stampers, pre-arm keys, grants, uploads, manifest, audit | the whole cancel ladder, ADR-045 watchdog, SPA span/step correlation |
| `UnifiedStore.mu` | **HIGH** | every `UnifiedStore` method | every session-writing subsystem; latency of unrelated sessions |
| `writeMetaLocked` | **HIGH** | `createSessionLocked`, `SetMeta` (31 call sites), `AppendTranscript`, `SwitchAgent` | goal/loop state machines, boot sweep, REST session payloads |
| `IsDelegateChildEntry` | **MEDIUM** | 4 read boundaries (5 effective, `rest.go` helper serves 2 handlers) | every rendered historical chat |
| `InterruptSession` family | **HIGH** | `RequestCancel`, `delegate action=cancel`, channel `/stop` | every cancellation path |
| `ApprovalGrantStore.Inherit` | **MEDIUM** | `spawnSubTurn`, `loop.go:8617`/`:8630-8631` | 300 s approval timeout inside every delegated child |
| `LifecycleFilter` | **MEDIUM** | `List`, `list_jobs` sources | durable cancel walk, `delegate action=inbox` |

**Risk note that is not a code-graph fact:** the impact figures above are counted from `rg`, not from a graph query. GitNexus `impact` on a struct *field* under-reports by default (it excludes `ACCESSES`); any implementer re-checking these numbers must pass `relationTypes` including `ACCESSES`, and must still treat the result as "what could behave differently", not "how many places must I edit".

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| Delegation spawn (`delegate.run` → `spawnSubTurn` → child turn) | The subject. Gains a real session create; identity, routing, grants, manifest all re-key |
| Chat Stop / `RequestCancel` PHASE A→B→C | Must keep reaching descendants after the id split (D2/D4) |
| ADR-045 orphan watchdog | Fire predicate condition 2 (`hasLiveCriticalDelegate`) depends on the shared id |
| Cancel pre-arm latch | Marker set/cleared under the parent's identity; preserved verbatim by `routingSessionID` |
| WS frame delivery → SPA bucketing | `session_id → bucket → parent_call_id → span → call_id → step`; hop 1 breaks without D2 |
| Transcript replay / cold load / verifier window / `inspect_session` | All four stop filtering (D6) |
| Session store write path (create, append, meta) | Striped, split, and throttled (D10/D11/D12) |
| Boot sweep / crash recovery | Must reconcile a child's lifecycle record across a deploy (AC-19) |

### Cluster Placement

Spans four clusters: **agent execution** (`pkg/agent`), **session storage** (`pkg/session`, `pkg/fileutil`, `pkg/media`), **tool surface** (`pkg/tools`, `pkg/security`), and **gateway/SPA boundary** (`pkg/gateway`, `contracts/`, `src/`). The storage cluster (D10/D11/D12 → W15/W23/W24) is separable from the identity cluster and is the one place a hard internal ordering applies.

---

## User Stories & Acceptance Criteria

### User Story 1 — A lost transcript write fails loudly (Priority: P0)

An engineer verifying any other story in this spec needs the transcript primitive to be honest. Today `AppendTranscript` against an unknown session id creates an orphan directory, writes the line, and returns `nil` (`pkg/session/unified.go:814` → `pkg/fileutil/file.go:207-210`, then `:819-823`). Until that changes, every acceptance criterion in this document is measured against a primitive that reports success for a lost write.

**Why this priority**: It is the gate. ADR-057 §10 consequence 3 states it directly — "AC-1 comes first and gates the rest." Landing any other work item first means measuring it with a broken instrument.

**Independent Test**: Call the strict primitive with a freshly generated UUID against a real `UnifiedStore` on `t.TempDir()`. Assert a non-nil error and assert `os.Stat` on the would-be directory returns `IsNotExist`. No other work item need exist.

**Acceptance Scenarios**:

1. **Given** a real `UnifiedStore` with no session `X`, **When** `AppendTranscriptStrict(X, entry)` is called, **Then** a non-nil error is returned **and** no directory `<baseDir>/X` exists on disk.
2. **Given** a real `UnifiedStore` with an existing session `Y`, **When** `AppendTranscriptStrict(Y, entry)` is called, **Then** it returns nil and `transcript.jsonl` grows by exactly one line.
3. **Given** a turn whose transcript store is wired and whose session id does not resolve, **When** any of the four `pkg/agent/turn.go` writers runs, **Then** the error is surfaced as a counter increment and a WARN naming the session id.
4. **Given** a turn marked `ts.abandoned`, **When** a transcript write is suppressed, **Then** the suppression emits a counted, logged signal rather than returning silently (`pkg/agent/turn.go:1296-1299` is silent today).
5. **Given** the compiled tree, **When** a distinct-type check runs, **Then** `SessionID` and `RoutingSessionID` are separate named types that do not interconvert implicitly.

---

### User Story 2 — A delegated child owns a real, store-backed session (Priority: P0)

A delegated child today carries two identity namespaces: its own `childID` (`pkg/agent/subturn.go:1020`) and the parent's `transcriptSessionID` (`:1034`), with `UnifiedStore.NewSession` never called for it (`pkg/tools/delegate.go:1248`). This story makes the child's own id its session id, its `sessionKey` and its `transcriptSessionID` — one namespace.

**Why this priority**: It is the ADR's central decision (D1) and the precondition for drill-down, per-child retention, `#564`, and the elimination of the #576/#577 defect class.

**Independent Test**: Run one delegation against a real store; assert `<baseDir>/<childID>/meta.json` exists and `GET /api/v1/sessions/{childID}` returns 200 with non-empty messages. Today that endpoint 404s.

**Acceptance Scenarios**:

1. **Given** a parent chat session and a delegation, **When** the child turn spawns, **Then** a session directory named exactly `childID` exists with a `meta.json`, created via the exact-id path (`pkg/session/unified.go:441`, precedent caller `:582`).
2. **Given** a parent session whose `meta.Owner` is a non-empty principal, **When** the child spawns, **Then** the child's `meta.Owner` equals the parent's verbatim, and `WithSessionOwner` installs inside the child turn (`pkg/agent/loop.go:6844-6848` guards on `meta.Owner != ""`).
3. **Given** the child's `processOptions`, **When** they are constructed, **Then** `NoHistory` is absent (today `true` at `pkg/agent/subturn.go:1032`) and `TranscriptSessionID == childID`.
4. **Given** a child session's meta, **When** it is read, **Then** `ParentSessionID` names the direct parent and the session type is the subordinate value.
5. **Given** a child turn, **When** `steer`/`respond`/`cancel`/`peek`/`inbox`/`follow_up` are invoked, **Then** each takes the same single id it takes today, because `delegateSessionID == sessionKey == transcriptSessionID`.

---

### User Story 3 — Client routing survives the identity split (Priority: P0)

The SPA buckets frames **strictly** by the frame's own `session_id`, with no chat check (`src/store/chat.ts:2883-2885`). `tool_call_start`/`tool_call_result` sit in `SESSION_SCOPED_FRAME_TYPES` alongside `subagent_start`/`subagent_end` (19 types, `:1236-1249`). Without an explicit routing key, a delegation's span lands in bucket `<chatSid>` while its steps land in `<childID>` — on the first delegation, on the live connection.

**Why this priority**: A 100 %-reproducible break of the primary delegation UI on the happy path, whose only signal is a dev-only diagnostic (`logDiagnostic('chatAttachStepSpanIndexMiss')`, `src/store/chat.ts:1959`).

**Independent Test**: Drive one delegation through the real gateway with a real WS client; assert the SPA store's `<chatSid>` bucket contains the span and its steps, and that the miss diagnostic never fires.

**Acceptance Scenarios**:

1. **Given** a root turn, **When** `routingSessionID` is read, **Then** it equals the turn's own session id, making root behaviour byte-identical to today.
2. **Given** a child turn, **When** `routingSessionID` is read, **Then** it equals the parent's verbatim, and the pre-arm latch keys set at `pkg/agent/subturn.go:585` are the ones cleared at `:1147`.
3. **Given** a delegation on a live connection, **When** `subagent_start`, `tool_call_start`, `tool_call_result` and `subagent_end` arrive, **Then** all four file into the `<chatSid>` bucket and the span/step correlation resolves.
4. **Given** any session-scoped frame produced by a child, **When** it crosses the wire, **Then** `session_id` is the routing key and `producing_session_id` is present and equal to the child's own id.
5. **Given** the non-test tree, **When** a consumer-set test enumerates reads of `routingSessionID`, **Then** every read is inside the closed set (WS payload stamping, the seven role-B predicates, pre-arm keys) and the test fails on any read outside it.

---

### User Story 4 — The parent→child edge is durable and queryable (Priority: P0)

A Stop must find a child that is no longer in memory. `OwnerScopeID` cannot serve: it is `""` for every direct child of a chat turn (`pkg/tools/delegate.go:1117-1122`; stated as contract at `pkg/session/lifecycle.go:141-143` and `:229`). `ParentDurableKey` is stamped unconditionally (`pkg/tools/delegate.go:1106`) and becomes a genuine strict-direct-parent edge under D1 — but `LifecycleFilter` has exactly five fields and `matches` explicitly refuses to match on it (`pkg/session/lifecycle.go:543-563`, `:571-575`), and `List` has no index.

**Why this priority**: Under D3 the lifecycle record becomes the **only** durable cancel edge. A missing store means a Stop cancels nothing, with no error.

**Independent Test**: Persist three real lifecycle records at depths 1–3, then query children-of-X by `ParentDurableKey` and assert exactly the direct children come back, in one file read.

**Acceptance Scenarios**:

1. **Given** lifecycle records for a chat, its child and its grandchild, **When** `List` is called with `ParentDurableKey` set to the chat id, **Then** exactly the direct child is returned — not the grandchild, not a sibling.
2. **Given** a session with N descendants, **When** the walk runs, **Then** its cost is O(descendants) via the parent index, not O(all sessions ever) per depth level (`pkg/session/lifecycle.go:617-636` is a full-directory scan plus full parse today).
3. **Given** a `DelegateTool` with no lifecycle store wired (`pkg/agent/session_messaging_wire.go:141-143` makes it optional today), **When** a delegation is attempted, **Then** it is **refused** with an operator-visible error, never a silent skip.
4. **Given** `tools.delegate.require_parent_agent_id=false`, **When** a child is minted with a blank `ParentAgentID`, **Then** the child is still reachable by the `ParentDurableKey` walk and a Stop cancels it.
5. **Given** the merged tree, **When** the three doc comments at `pkg/session/lifecycle.go:225-228`, `:571-575` and `pkg/tools/list_jobs_sources.go:311-315` are read, **Then** none of them describes `ParentDurableKey` as shared between parent and children.

---

### User Story 5 — A Stop reaches the whole subtree, live and durable (Priority: P0)

Five shipped safety mechanisms were built specifically to exploit the shared transcript id and say so in their doc comments. Without the routing key they all return "nothing to do" and every caller proceeds: the escalation ladder (`pkg/agent/steering.go:730-733`), the ADR-045 interlock (`pkg/agent/orphan_watch.go:280-287`), the pre-arm latch (`pkg/agent/cancel_prearm.go:385-389`), background-shell reaping (`pkg/agent/cancel.go:233-234`), and the `turn_canceled` audit descendant list.

**Why this priority**: An un-hard-aborted child "retries with a fresh, uncanceled context and keeps running — invisibly, for as long as its own task takes" (`pkg/agent/steering.go:730-733`).

**Independent Test**: Register a real root that finishes gracefully and a real `Critical:true` child that does not; issue a real Stop; assert PHASE B hard-abort and PHASE C detach both fire against the child (`pkg/agent/cancel.go:462`, `:487`).

**Acceptance Scenarios**:

1. **Given** a real root turn and a real live child turn, **When** a Stop is issued, **Then** PHASE A computes the live subtree once and PHASE B and PHASE C consume that set rather than re-scanning.
2. **Given** a Stop that reached a child, **When** the `turn_canceled` audit entry is read, **Then** `descendants_canceled` (`pkg/agent/cancel.go:376`) is non-empty and names the child.
3. **Given** a live `Critical:true` async delegate and an orphaned root, **When** the ADR-045 watchdog evaluates its fire predicate, **Then** it does **not** fire, and it does fire once the delegate finishes.
4. **Given** a child that started a background `bash`, **When** a chat-level Stop is issued, **Then** the real PID is gone; **and** a sibling's background shell survives.
5. **Given** a `delegate action=cancel` on that child, **When** it executes, **Then** the child's background shells are killed (today `InterruptBySessionKey` never calls `KillBackgroundSessions` at all — the only non-test call site tree-wide is `pkg/agent/cancel.go:234`).
6. **Given** a Stop, **When** the durable walk runs, **Then** each descendant's lifecycle record transitions to `cancelled` (today `pkg/agent/cancel.go:428` transitions exactly one).

---

### User Story 6 — Approvals inherit to the child and are torn down with it (Priority: P0)

`Inherit` writes under `{sessionID, agentID}` (`pkg/security/approvalgrants.go:112-123`), written at spawn with the parent's transcript id (`pkg/agent/subturn.go:916`) and read inside the child with the child's (`pkg/agent/loop.go:8617`, `:8630-8631`). Under D1 without a decision, every inherited grant misses, the child falls through to `CheckGrantOrRequestApproval` and blocks on a human for up to 300 s per tool call — with the delegate span hidden from the thread unless verbose chat is on (`src/lib/toolVisibility.ts:218-223`). The symptom is a delegation that hangs for five minutes with no prompt and no explanation.

**Why this priority**: The failure direction is safe but the availability impact is severe and invisible.

**Independent Test**: With a standing grant on the parent, run one delegation and assert the child executes the granted tool with no approval prompt and no wait.

**Acceptance Scenarios**:

1. **Given** a standing grant on the parent for tool `T`, **When** a child executes `T`, **Then** no approval prompt is raised and no 300 s wait occurs.
2. **Given** the grant now keyed `{childSessionID, childAgentID}`, **When** the child session terminates, **Then** the grant set is gone — the grant does not outlive the child.
3. **Given** a pending approval inside a child, **When** a chat-level Stop is issued, **Then** the registry entry is gone, its timer is stopped, and the child's goroutine unblocks.
4. **Given** a terminated child turn, **When** teardown runs, **Then** `CloseSession` has run for the child: its grant set, `loadedTools` bucket and `recallSpans` entries are gone (today no call site exists on any child/delegate path).

---

### User Story 7 — The transcript visibility filter is deleted outright (Priority: P1)

Under D1 a child's entries are written to the child's own `transcript.jsonl`, so the content-based predicate `IsDelegateChildEntry() { return e.ParentSpawnCallID != "" }` (`pkg/session/daypartition.go:332-334`) has nothing to match for any session created after cutover. Greenfield removes the reason to carry it at four sites (five effective read boundaries — `pkg/gateway/rest.go`'s helper serves both `getSession` and `getSessionMessages`).

**Why this priority**: High value, low risk under greenfield, but strictly downstream of D1 landing; deleting the filter before D1 would un-hide narration with nothing gained.

**Independent Test**: Assert `IsDelegateChildEntry` has zero non-test references and that after one delegation the **parent's** `transcript.jsonl` contains no child entry — asserted on the file, so the property cannot be satisfied by a re-added filter.

**Acceptance Scenarios**:

1. **Given** the merged tree, **When** a repo-wide reference check runs, **Then** `IsDelegateChildEntry` has zero references outside tests and none of the four read boundaries filters on `ParentSpawnCallID`.
2. **Given** one delegation, **When** the parent's `transcript.jsonl` is read from disk, **Then** it contains no child entry at all.
3. **Given** the child's own session, **When** `inspect_session` and `GET /api/v1/sessions/{childID}` are called, **Then** both return the full transcript, unfiltered.
4. **Given** a child's own transcript entries, **When** they are read, **Then** `ParentSpawnCallID` is still stamped as provenance and is read by the drill-down surface.
5. **Given** an adjudication window, **When** the verifier renders it (`pkg/agent/verifier_adjudication.go:403`), **Then** it receives the adjudicated session's own entries and nothing else.
6. **Given** a **pre-cutover** session that ran a delegation, **When** it is rendered, **Then** previously-hidden delegate narration appears as top-level bubbles — **accepted**, bounded to pre-cutover sessions (R-16).

---

### User Story 8 — Ownership is an ancestor-chain walk, not subtree-wide equality (Priority: P1)

`callerOwnerKey` returns `ToolTranscriptSessionID(ctx)` (`pkg/tools/delegate.go:1966-1968`) and is compared for equality against `rec.ParentDurableKey` (`:1973-1979`). Because every descendant inherits the root chat's transcript id today, the gate is chat-subtree-wide: a parent can address its grandchildren, **and a child can address its siblings and cousins**.

**Why this priority**: Closing the sibling/cousin leak is a genuine security improvement, but it depends on D1 and D3 having landed.

**Independent Test**: At depth 3, assert a sibling cannot `cancel`/`steer`/`peek` another sibling, while the root chat still can reach a grandchild.

**Acceptance Scenarios**:

1. **Given** a chat with two children B and C, **When** B attempts any of the six gated actions against C, **Then** the action is rejected.
2. **Given** a chat, its child B and B's child D, **When** the chat issues a gated action against D, **Then** it is permitted (root-over-subtree preserved).
3. **Given** a delegation chain deeper than the configured max delegation depth, **When** the walk runs, **Then** it terminates at the bound and rejects rather than looping.
4. **Given** all six gated call sites (`pkg/tools/delegate.go:2010`, `:2107`, `:2159`, `:2321`, `:2459`, `:2592`), **When** each is exercised, **Then** each uses the walk — none retains equality.

---

### User Story 9 — One interrupt entry point with an explicit scope (Priority: P1)

`InterruptSession(sessionID, hint)` (`pkg/agent/steering.go:449`) and `InterruptBySessionKey(sessionKey, hint)` (`:611`) have identical Go signatures and differ only in cascade semantics. Today they are distinguishable because they take ids from different namespaces. After D1 they take the same id — recreating the confusion class this ADR eliminates, on the cancel path, in the code `#577` just fixed. The hazard is already flagged by name at `pkg/tools/delegate.go:556-561`.

**Why this priority**: Prevents the migration from regenerating the defect it exists to remove.

**Independent Test**: Assert the compiler rejects an interrupt call that does not name a scope, and that `Interrupt(childB, ScopeSubtree)` leaves parent A and sibling C running.

**Acceptance Scenarios**:

1. **Given** the collapsed API, **When** any caller invokes an interrupt, **Then** it must supply an explicit `InterruptScope` — the compiler enforces it.
2. **Given** a chat A with children B and C, and B with its own child D, **When** `Interrupt(B, ScopeSubtree)` runs, **Then** B and D are cancelled and A and C keep running.
3. **Given** the same tree, **When** `Interrupt(chat, ScopeSubtree)` runs, **Then** all three depths are reached.
4. **Given** `pkg/agent/interrupt_by_session_key_test.go:9-19,232`, **When** the change lands, **Then** the test is **deliberately inverted** to assert the new invariant — not deleted.

---

### User Story 10 — Delegate status and activity stop returning silent emptiness (Priority: P1)

`delegateStatusExtra` calls `recentActivityLines(task.SessionID, …)` (`pkg/tools/delegate.go:1823`) with a documented silent-nil path (`:1844-1851`), reading the parent's transcript and finding nothing. Separately, `executeSync` registers no `DelegateTaskState` at all (`:1507`; only `executeAsync` does, `:1280`/`:1315`), so `status`'s activity snapshot is already absent for every synchronous delegation. And nothing anywhere deletes from `t.tasks` or `t.sessionIndex` — both grow for the process lifetime.

**Why this priority**: Observability regression that would otherwise be attributed to this migration; also a genuine unbounded-growth defect.

**Independent Test**: Call `delegate action=status` after a **sync** delegation and assert a non-empty activity snapshot.

**Acceptance Scenarios**:

1. **Given** a completed synchronous delegation, **When** `delegate action=status` is called, **Then** a non-empty activity snapshot is returned.
2. **Given** a completed asynchronous delegation, **When** `delegate action=status` is called, **Then** a non-empty activity snapshot is returned.
3. **Given** a delegation whose activity genuinely is empty, **When** `recentActivityLines` returns nothing, **Then** the empty path is logged rather than returning silently.
4. **Given** N completed delegations, **When** the process continues, **Then** `t.tasks` and `t.sessionIndex` do not retain all N entries indefinitely.

---

### User Story 11 — Concurrent sessions stop serialising on one store-global lock (Priority: P1)

`UnifiedStore` has a single non-striped `sync.RWMutex` (`pkg/session/unified.go:161`). `NewSession` takes the **write** lock (`:415-416`) and holds it through `os.MkdirAll` (`:463`), `writeMetaLocked` (`:466`) and a second `WriteFileAtomic` (`:472`) — each `WriteFileAtomic` doing a file `Sync()` (`pkg/fileutil/file.go:97`) **and** a parent-directory `Sync()` (`:121`). `AppendTranscript` takes the same write lock on **every streamed line** (`:810-811`), and `ListSessions` takes it too (`:1247`). After D1 every delegation is an fsync-bound session create behind that lock.

**Why this priority**: D1 is what makes this load-bearing. Without it, a 24-way fan-out serialises 24 fsync-bound creates and stalls token streaming in every other session in the store.

**Independent Test**: N goroutines each create a session and append to their own session against a real on-disk store; assert wall-clock is close to single-session time and that doubling N does not double the time.

**Acceptance Scenarios**:

1. **Given** N concurrent writers on N distinct sessions against a real on-disk store, **When** they create and append, **Then** the slope holds: doubling N does not double wall-clock, measured against the pre-change store as the baseline it must beat.
2. **Given** an in-flight `NewSession` on session A, **When** `ListSessions` is called, **Then** it does not block on A.
3. **Given** a streaming append loop on session A, **When** session B is created, **Then** A's inter-token latency is unaffected.
4. **Given** concurrent create/append/`SetMeta`/`ListSessions`/`DeleteSession` on overlapping and disjoint ids, **When** run under `-race`, **Then** the run is clean; `ClearAll`/`RetentionSweep` interleaved with per-session writes neither deadlock nor drop a session.
5. **Given** the merged tree, **When** `cacheMu` critical sections are inspected, **Then** none contains an `os.*` or `fileutil.*` call, and lock order is only ever `sessionLock(id)` → `cacheMu`.

---

### User Story 12 — `meta.json` splits into four files, one per writer family (Priority: P1)

`SessionMeta` is one document holding identity+lifecycle (`pkg/session/daypartition.go:77-104`), an embedded `SessionStats` (`:85`; 9 fields, type at `:209-223`), **9** `Goal*` fields and **9** `Loop*` fields. `writeMetaLocked` marshals the **whole** document on every mutation (`pkg/session/unified.go:786-799`). Today a `/loop` tick rewrites the goal state machine, a `/goal` judge round rewrites `LoopJobID`, and a single streamed token rewrites both.

**Why this priority**: It is what makes the D12 throttle safe (Alternative F is rejected precisely because a flusher over a fused document either clobbers or re-serialises).

**Independent Test**: After a create plus one `/goal set`, one `/loop` start and one transcript append, assert the session directory holds exactly `meta.json`, `stats.json`, `goal.json`, `loop.json`, each containing only its own group's fields.

**Acceptance Scenarios**:

1. **Given** a session exercised on all four write paths, **When** its directory is listed, **Then** four files exist and each contains only its own group's fields.
2. **Given** a `/loop` tick, **When** it completes, **Then** `goal.json`'s bytes are unchanged; symmetrically for a `/goal` round against `loop.json`; and a transcript append leaves both unchanged.
3. **Given** a session directory with `meta.json` only, **When** it is loaded, **Then** it loads successfully with zero-valued stats/goal/loop.
4. **Given** a session directory with **no** `meta.json`, **When** it is loaded, **Then** `readUnifiedMeta` returns an error and `GET /api/v1/sessions/{id}` 404s.
5. **Given** a present but truncated/corrupt `goal.json`, **When** it is loaded, **Then** an error surfaces for that group rather than silently composing a zero goal.
6. **Given** the same logical state before and after the split, **When** `UnifiedMeta` is marshalled and every REST/WS payload is rendered, **Then** the bytes are identical and `make verify-contracts` is unaffected.
7. **Given** the merged tree, **When** `writeMetaLocked`'s (`:780-785`) and `metaCache`'s (`:166-181`) doc comments are read, **Then** neither asserts a single whole-document write funnel.

---

### User Story 13 — The per-token counter write is throttled; every event-driven write stays immediate (Priority: P1)

`AppendTranscript` bumps `Stats.*` (`pkg/session/unified.go:824-846`) and `UpdatedAt` (`:847`) then rewrites the whole meta document (`:848`) **once per streamed transcript line** — a marshal, a `WithFlock`, an fsync, a rename and a directory fsync per token. The system already treats that write as expendable: it returns `nil` when the meta read fails (`:819-823`) and `nil` when the meta write fails (`:848-856`).

**Why this priority**: Directly downstream of US-12; must not land without it.

**Independent Test**: Burst appends within one flush interval against a real store; assert `stats.json`'s mtime and bytes do not change while `transcript.jsonl` grows by exactly one line per append.

**Acceptance Scenarios**:

1. **Given** a burst of appends inside one flush interval, **When** the burst completes, **Then** `stats.json`'s mtime and bytes are unchanged and `transcript.jsonl` has exactly one new line per append.
2. **Given** the flush interval has elapsed, **When** `stats.json` is read, **Then** it matches the counters implied by the appended entries **exactly** — no lost or double-counted delta.
3. **Given** each forced flush point in turn — a `SetMeta` carrying `Status`, `DeleteSession` (`:1397`), `UnifiedStore.Close` (`:1388`), and the child `CloseSession` — **When** it fires, **Then** `stats.json` is current and re-opening the store reads back the exact counters.
4. **Given** a `/goal` round, a `/loop` tick, a `Status` transition and a `Title` change, **When** each call returns, **Then** its value is on disk **immediately**, with no flush interval elapsed.
5. **Given** two sessions where B streamed most recently, **When** `ListSessions` is called with no flush in between, **Then** B sorts ahead of A.
6. **Given** the process is killed mid-interval, **When** the store is re-opened, **Then** the counters are behind by at most that interval's appends and the transcript is complete.

---

### User Story 14 — A child session is inspectable, and the session list scales (Priority: P2)

`GET /api/v1/sessions/{childID}` 404s today (`pkg/gateway/rest.go:834-844`, no `UnifiedMeta`). The ActivityPanel is not a usable fallback: `subagent_message`/`subagent_state` have **zero Go emitters** and are absent from the `WsFrameType` enum in contracts, Go and TS. Meanwhile there is no pagination at any layer and the sidebar shows only the 9 most recent by recency — so 24 child sessions evict the parent chat.

**Why this priority**: Required to make hidden delegations inspectable at all, but not on the correctness-critical path.

**Independent Test**: Without verbose chat enabled, open `GET /api/v1/sessions/{childID}` for a hidden delegation and render it; assert the transcript is populated.

**Acceptance Scenarios**:

1. **Given** a hidden delegation and verbose chat **disabled**, **When** the drill-down surface is opened by child id, **Then** it is reachable and populated using only `GET /api/v1/sessions/{childID}`.
2. **Given** a store with more sessions than one page, **When** `GET /api/v1/sessions` is called, **Then** it paginates through all four layers.
3. **Given** a 24-way fan-out under one parent chat, **When** the sidebar renders, **Then** the parent chat is still shown.
4. **Given** the drill-down view, **When** it filters, **Then** it filters on `producing_session_id`, and no criterion depends on `subagent_message`/`subagent_state`.

---

### User Story 15 — Root-level delegation fan-out is admission-gated (Priority: P2)

`AdmissionController` gates inbound user-message dispatch only and says so verbatim: *"Subagent spawn and task-executor dispatch paths are NOT gated"* (`pkg/agent/admission.go:12-18`). `turnState.concurrencySem` is set **only** on a child (`pkg/agent/subturn.go:1051`), so nested delegation is gated and root-level delegation is not — matching the live "24 parallel against a cap of 16" observation in `docs/internal/uat/max-parallel-concurrency-gap-2026-07-31.md` §G1.

**Why this priority**: Required **by this ADR** — D1 turns every delegation into an fsync-bound session create, so an ungated root fan-out becomes a self-inflicted DoS. Not required for correctness of any other story.

**Independent Test**: With the gate configured to N, attempt N+1 concurrent root-level delegations and assert the N+1th is refused rather than queued behind the store lock.

**Acceptance Scenarios**:

1. **Given** a configured root-delegation cap of N, **When** the N+1th concurrent root-level delegation is attempted, **Then** it is **refused**, not queued.
2. **Given** the gate is in effect, **When** a nested (child-level) delegation runs, **Then** its existing `concurrencySem` behaviour is unchanged.
3. **Given** the refusal, **When** it surfaces, **Then** it is operator-visible, not silent.

---

### User Story 16 — Child upload directories are reachable by cascade-delete (Priority: P2)

Tool-media uploads resolve their directory from `ToolTranscriptSessionID(ctx)` (`pkg/tools/normalization.go:247-248` → `pkg/media/tempdir.go:33-51`) and use `CleanupPolicyForgetOnly` (`pkg/tools/normalization.go:254`), which is immune to the TTL cleaner. Today parent and children share one directory; after D1 each child gets its own, and nothing deletes it.

**Why this priority**: A silent disk leak, not a correctness break.

**Independent Test**: Run a delegation that uploads media, delete the parent session, assert `<home>/uploads/<childID>/` is gone.

**Acceptance Scenarios**:

1. **Given** a parent session with a descendant that uploaded media, **When** the parent session is deleted, **Then** `<home>/uploads/<childID>/` is removed for **every** descendant.
2. **Given** a child id that is path-unsafe, **When** the uploads directory is resolved, **Then** the existing `("", false)` rejection (`pkg/media/tempdir.go:34-44`) still applies.

---

### User Story 17 — The suite's encoding of the old contract is inverted deliberately, in its own commit (Priority: P0)

Roughly 71 test files and ~430 references touch the shared-transcript-id value (128 `transcriptSessionID` refs across 43 test files alone). Twelve named files pin the current contract explicitly and all twelve exist today. The suite **is** the specification of the current contract; quietly deleting a gate test converts a contract change into an untracked behaviour change.

**Why this priority**: P0 because it is the difference between "the contract changed" and "the behaviour regressed" being distinguishable under bisection — and because R-4/R-5's failures are the same silent shape #576–#588 were.

**Independent Test**: `git log` shows W22's inversions as a commit containing only `*_test.go` changes, with no behaviour file in the same commit.

**Acceptance Scenarios**:

1. **Given** each of the twelve named gate tests, **When** the change lands, **Then** each asserts the **new** invariant — none is deleted and none is left asserting the old one.
2. **Given** the commit history, **When** it is inspected, **Then** W22's test inversions are a single commit containing no behaviour-file change.
3. **Given** every test written or inverted for this spec, **When** it constructs parent and child ids, **Then** they are distinct, non-equal values and the assertion names which one was used.

---

### User Story 18 — Consequential semantics that change are pinned by assertion, not assumed (Priority: P1)

Five behaviours change as a **consequence** of D1–D8 rather than as a target of them. ADR-057 names each and requires each to be asserted: `follow_up` warm resume now sees the previous generation's history (R-11); ADR-053 D15's per-child message ceiling becomes per-direct-parent, so a chat's aggregate is (children × ceiling); ADR-053 D16's inbox routing moves from the chat to the immediate parent, and producer and consumer must move together or `delegate action=inbox` returns a clean, empty success payload forever; a 3P child's own sub-delegations are outside the session graph, so the process group is the only cancellation boundary; and a deploy landing mid-delegation must not leave an orphan directory.

**Why this priority**: Each is a silent-failure candidate — an empty inbox, a ceiling that is quietly 3× wider, a surviving foreign process tree, a transcript in a directory nothing knows about. None blocks another story, but each would ship undetected.

**Independent Test**: Build a depth-3 tree and assert inbox drain, message ceiling, `follow_up` resume, 3P process-group death and restart reconciliation independently of one another.

**Acceptance Scenarios**:

1. **Given** a completed child, **When** `follow_up` resumes it, **Then** generation N's history is visible in generation N+1's first assembled message list — the intended behaviour, not a leak (R-11, AC-11).
2. **Given** a chat with C children each at the per-child message ceiling, **When** the aggregate is measured, **Then** it equals (C × ceiling), enforced per **direct parent** at depth 3 (ADR-053 D15, AC-15).
3. **Given** a depth-3 tree, **When** the grandchild calls `message_parent`, **Then** its direct parent's `delegate action=inbox` drains it and no other node's does (ADR-053 D16, AC-16).
4. **Given** an external-CLI (3P) child running its own subprocess tree, **When** the child is cancelled, **Then** its process group dies and the subtree dies with it (D3 gap 5, AC-17c).
5. **Given** a parent turn mid-delegation, **When** the process restarts, **Then** the boot sweep reconciles the child's lifecycle record and no transcript write lands in an orphan directory (AC-19).

---

## Behavioral Contract

**Primary flows**

- When a delegation is dispatched, the system creates a store-backed session whose id is exactly the child id, whose `meta.Owner` is the parent's, and whose `ParentSessionID` names the direct parent.
- When a child turn writes a transcript entry, the entry lands in the child's own `transcript.jsonl` and never in the parent's file.
- When any session-scoped WS frame leaves the gateway, its `session_id` is the routing key inherited from the root of the chat tree, and `producing_session_id` is present iff it differs.
- When a Stop is issued on a chat, the live subtree is computed once from the routing key and hard-abort, detach, background-shell kill, pending-approval cancel and lifecycle transition all apply to that whole set.
- When a `delegate action=cancel` targets child B, exactly B and B's own descendants are cancelled — never the parent, never a sibling.
- When a gated delegate action (`inbox`, `steer`, `respond`, `cancel`, `follow_up`, `peek`) is invoked, it is permitted iff the caller is an **ancestor** of the target within the configured depth bound.
- When a transcript is read at any of the four read boundaries, it is returned unfiltered.
- When a session writes any of identity, statistics, goal state or loop state, it writes exactly one of the four files and leaves the other three byte-unchanged.
- When a transcript line is appended, the transcript write is immediate and the counter update is in memory only.

**Error flows**

- When a transcript append targets a session id with no `meta.json`, the call returns a non-nil error and creates no directory; the caller surfaces it as a counter increment and a WARN.
- When a delegation is attempted with no lifecycle store wired, the delegation is refused with an operator-visible error.
- When the ancestor walk exceeds the configured max delegation depth, the action is rejected.
- When `meta.json` is absent, the session load returns an error and the REST surface 404s.
- When `goal.json` / `loop.json` / `stats.json` is present but corrupt, the load surfaces an error for **that group**.
- When the root-level delegation admission cap is reached, the next root-level delegation is refused with an operator-visible error.

**Boundary conditions**

- When a turn is a **root** turn, `routingSessionID` equals its own session id and every downstream behaviour is byte-identical to today.
- When a delegation is a **self-delegation**, the child is still a distinct session with a distinct id; the grant `Inherit` is a same-agent, different-session union.
- When `goal.json` / `loop.json` / `stats.json` are **absent**, they compose as the zero value and the load succeeds.
- When a session's last activity was a stream that never reached a flush point, its `UpdatedAt` on reload is stale by at most one flush interval; within a live process, ordering is exact.
- When `require_parent_agent_id=false` blanks `ParentAgentID`, `ParentDurableKey` is still stamped and the walk still reaches the child.

---

## Edge Cases

- **A child spawns while the parent's Stop is already in flight.** Expected: the pre-arm latch, keyed on the parent identity the child inherits verbatim as `routingSessionID`, is consumed by the child — not expired (`pkg/agent/cancel_prearm.go:338`, `:355`, `:385-389`; markers at `pkg/agent/subturn.go:585`, `:1147`).
- **A process restart lands mid-delegation.** Expected: the boot sweep reconciles the child's lifecycle record and no transcript write lands in an orphan directory (AC-19).
- **`follow_up` warm resume reuses `childID` verbatim** for the next generation (`pkg/agent/subturn.go:1115-1135`). With `NoHistory: false` and a real session behind that id, generation N+1 loads generation N's history. Expected: **that is intended** — a corrective follow-up should see what it is correcting (R-11).
- **An external-CLI (3P) child's own sub-delegations never reach the lifecycle mint.** Expected: out of the session graph by construction; the boundary is the 3P child's own process-group kill, and the subtree dies with it.
- **A grandchild's `message_parent` output.** Expected: `ParentDurableKey` now names the **immediate** parent, so it routes to the direct parent's inbox — producer (`pkg/tools/message_parent.go:640`) and consumer (`pkg/tools/delegate.go:2024`, `:2200`) must move together or `delegate action=inbox` returns a clean, empty success payload forever (AC-16).
- **The ADR-053 D15 per-child message ceiling.** Expected: it becomes per-direct-parent instead of per-chat-subtree, so a chat's aggregate is (children × ceiling) rather than one shared pool — **asserted, not assumed** (AC-15).
- **A channel `/stop` arrives when only a surviving child is alive.** Expected: `resolveSessionIDByChannelChat` (`pkg/agent/turn.go:557-583`) returns the **routing** id, so the Stop cancels the tree — not just the child.
- **Two sessions collide on an FNV-32a hash mod 64.** Expected: they contend on one shard; correctness is unaffected and throughput is bounded by the filesystem and the admission gate, never by the shard count.
- **`ClearAll` / `RetentionSweep` run while per-session writes are in flight.** Expected: every shard is taken **in index order** (never hash order); no deadlock, no dropped session.
- **A `SetMeta` carrying `Status` lands between a counter bump and a flush.** Expected: structurally unrepresentable as a clobber — the flusher owns `stats.json`, which no other writer touches.
- **A pre-cutover session that ran a delegation is rendered.** Expected: previously-hidden narration appears; accepted and bounded (R-16). Tool-call and error entries were never filtered anyway — only three writers stamp `ParentSpawnCallID` (`pkg/agent/turn.go:1204`, `:1268`, `pkg/gateway/websocket.go:4254`), while `appendToolCallTranscript` (`pkg/agent/turn.go:1123-1129`) and `appendErrorTranscript` (`:1314-1324`) do not.
- **`HydrateAgentHistoryFromTranscript` on reload.** Expected: the parent agent's LLM context **stops** absorbing delegate narration (`pkg/agent/attach_hydrate.go:34-42`, zero filter references, run at `pkg/gateway/websocket.go:2577` and `pkg/agent/loop.go:6204`). This is a behaviour change to the parent's own context and reviewers must see it coming.
- **A child hands off.** Expected: impossible — `hand_off` is structurally excluded from a child registry (`pkg/agent/subturn.go:988` → `registry.go:667-669`), so `sessionActiveAgent` correctly returns `""` and the delegate target is stamped.
- **A child's `recallSpans` cleanup key.** Expected: `forgetSession` matches via `key == sessionID || strings.HasSuffix(key, ":session:"+sessionID)` (`pkg/agent/loop.go:11497-11500`); a child's `sessionKey` is a bare UUID, so the first arm matches — provided something actually calls `CloseSession`.

---

## Explicit Non-Behaviors

- The system MUST NOT use `routingSessionID` as a session-store key, a transcript write target, an ownership predicate, a steering-queue scope, an approval-grant key, an uploads-directory key, a tool-manifest bucket, a lifecycle-record field, or an audit `session_id`. This exclusion list is enforced by test (AC-2).
- The system MUST NOT infer parentage from `OwnerScopeID` — it is `""` for every direct child of a chat turn, and a task dispatch puts a **plan id** in it (`pkg/agent/task_executor.go:202-208`, `:224-233`), so a walk over it would mistake a plan id for a session id.
- The system MUST NOT infer parentage from `ParentAgentID` — it is an agent config id, so two chats where the same agent delegates are indistinguishable.
- The system MUST NOT throttle any event-driven `SetMeta` path (goal, loop, status, title, owner, workspace). They are control flow, not display: a judge round reads back `GoalRoundsUsed`/`GoalMaxRounds` to decide whether to continue, `/loop stop` needs `LoopJobID` to find the cron job, and `boot_sweep.go:321` transitions `Status` for crash recovery. Throttling any of them reintroduces the ADR-037 anti-pattern this project bans — a control that reports success and changes nothing.
- The system MUST NOT change plan cancellation. `StopPlan` (`pkg/agent/plan_engine.go:2044-2135`) already builds an explicit `[]string` under `planDecisionMu` and calls `RequestCancelForSession` once per id (`:2330-2385`). No change.
- The system MUST NOT unify `turnState.concurrencySem`, `TaskExecutor.dispatchSema` and `TaskExecutor.maxConcurrent`. That cut is ratified; the single exception is W17's root-level gate. D12's *write-cadence* throttle shares a word with this and nothing else.
- The system MUST NOT mint the child's `UnifiedMeta` lazily on first drill-down. Between spawn and first drill-down the child would write into a directory with no meta — invisible to `ListSessions`, to replay and to `GET /api/v1/sessions/{id}` — while every write returns `nil`. That is R-7 reborn and it makes AC-1 unassertable.
- The system MUST NOT keep the counters in the fused `meta.json` while throttling them (Alternative F). The flusher would clobber goal/loop/status or re-serialise everything under a lock shared with all 31 event-path call sites.
- The system MUST NOT change `UnifiedMeta`'s in-memory shape or its marshalled JSON. **No `contracts/` change and no regeneration are required by D11/W23.** (W5 *does* require the Constraint #8 pipeline — that is a different work item.)
- The system MUST NOT re-add a transcript visibility filter anywhere, including in frontend code. AC-18(b) asserts the property on the **file**, so a re-added filter cannot satisfy it.
- The system MUST NOT rely on `subagent_message` / `subagent_state` frames. They have zero Go emitters, are absent from the `WsFrameType` enum in contracts, Go and TS, and their structs are dead declarations (`pkg/api/generated/asyncapi_types.gen.go:496`, `:521`).
- The system MUST NOT touch `migrateLegacy` / `writeUnifiedMetaDirect` (`pkg/session/unified.go:1515`) — they handle a *different* legacy (PartitionStore → UnifiedStore) and are out of scope.
- The system MUST NOT provide a reader for a pre-split fused `meta.json`. Greenfield: a fresh install writes four files from `createSessionLocked` onward and never encounters the old shape.

---

## Integration Boundaries

### Gateway ↔ SPA (WebSocket frames)

- **Data in**: session-scoped WS frames emitted by `pkg/agent` payload types.
- **Data out**: `session_id` (routing key, always present on session-scoped frames) and a new **optional** `producing_session_id`, present iff it differs from `session_id`.
- **Contract**: `contracts/asyncapi.yaml` + `contracts/components/schemas/`. Constraint #8's 5-step pipeline is mandatory: schema → reference → `scripts/gen-contracts.sh` → commit the generated diff in the **same** commit → write the consumer against the generated type only. Hand-written wire types are lint-caught.
- **On failure**: the SPA edge validates every incoming payload against the generated zod schema; a failure drops the frame, increments a counter, and shows a dev-mode toast. No production crash.
- **Development**: real gateway. AC-3 explicitly forbids satisfying this boundary with a mocked socket — the assertion is on the SPA store's bucket membership on a live connection.
- **Known pre-existing strain (not caused here, must not be attributed here)**: `RateLimitPayload` has no `SessionID` field at all (`pkg/agent/events.go:525-533`) and its `session_id` is reconstructed from the connection's chat→session map (`pkg/gateway/websocket.go:3461` → `sessionIDForChat`, `:3022`), so a reconstructed `""` is dropped in production; and `'replay_done'` is in `SESSION_SCOPED_FRAME_TYPES` but absent from the `WsFrameType` enum on both sides.

### Gateway ↔ SPA (REST)

- **Data in**: `GET /api/v1/sessions` (list, now paginated), `GET /api/v1/sessions/{id}` (detail — must resolve for a child id).
- **Data out**: session list/detail wire shape (`pkg/gateway/rest.go:608-665`) plus the new subordinate session type and `ParentSessionID`.
- **Contract**: `contracts/openapi.yaml`. The `verifier` session type is the working precedent: it required a store enum, an OpenAPI enum and an SPA change (`pkg/gateway/rest.go:783-785` + `?include_verifier=true`).
- **On failure**: `GET /api/v1/sessions/{id}` 404s when no `UnifiedMeta` resolves (`pkg/gateway/rest.go:834-844` ← `pkg/agent/loop.go:5012-5039`). Under D11 that 404 is the **required** behaviour for a directory with no `meta.json` (AC-21c).
- **Development**: real gateway binary, not the Vite dev server.

### Agent loop ↔ session store

- **Data in**: exact session id + transcript entry / meta patch.
- **Data out**: `error` (now non-nil for an unknown session on the strict path).
- **Contract**: `AppendTranscriptStrict` returns a non-nil error and creates no directory for an unresolvable id. `CreateSessionWithID` creates with the exact supplied id and copies the parent's `Owner`.
- **On failure**: caller surfaces a counter increment plus a WARN naming the session id. `ts.abandoned` suppression is counted and logged, not silent.
- **Development**: real `UnifiedStore` on `t.TempDir()`. Fakes are disallowed for every AC.

### Agent loop ↔ lifecycle store

- **Data in**: `LifecycleRecord` with `ParentDurableKey` stamped unconditionally (`pkg/tools/delegate.go:1106`, `:1173`).
- **Data out**: `List(LifecycleFilter{ParentDurableKey: X})` → the direct children of X, via a secondary parent index.
- **Contract**: the index is maintained **inside `Persist`**, under the existing 64-shard striped lock (`pkg/session/lifecycle_lock.go:19-31`; precedent `pkg/session/message_inbox.go:135-139`).
- **On failure**: **fail closed.** No lifecycle store wired → delegation refused (mirroring the existing fail-closed posture at `pkg/tools/delegate.go:1150-1157`). Never a silent skip.
- **Development**: real on-disk lifecycle store.

### Agent loop ↔ background process manager

- **Data in**: `ProcessSession.OwnerSessionID`, stamped from the owning session (`pkg/tools/shell.go:571-572` → `:1035`).
- **Data out**: `KillAllForSession` matches on it (`pkg/tools/session.go:455`).
- **Contract**: the stamp becomes the child's **own** id; kill cascades over the descendant set; `delegate action=cancel` kills that child's shells.
- **On failure**: a 3P child's process **group** must die with the child — asserted, because its own sub-delegations are outside the Omnipus tool surface.
- **Development**: real processes with real PIDs; assert the PID is gone.

### External CLI (3P) subagents

- **Data in**: task prompt; the child runs inside a foreign CLI's process tree.
- **Data out**: transcript entries via `pkg/agent/external_dispatch.go:463`, `:550-555`, `:562-564`.
- **Contract**: out of the lifecycle session graph by construction. The cancellation boundary is the process group.
- **On failure**: if the process group survives, the subtree survives — AC-17(c) asserts it does not.
- **Development**: real subprocess.

---

## Work Unit Decomposition & File Ownership

> **This table is a safety mechanism, not bureaucracy.** This repository is a **shared working tree** and this session has already observed concurrent agents silently reverting each other's edits. A unit that writes a file it does not own can destroy another unit's work with no error and no conflict marker.

**Rules**

1. **A file has exactly one owner.** Where a file appears against a *chain* (`U4→U5→U6`), the chain is the owner and its members **must never run concurrently**.
2. **A unit that needs a change in a file it does not own must request it from the owner** — it must not make the edit itself.
3. **`git add` only the files your unit owns.** Run `git status --short` first, every time.
4. **Generated artefacts** (`pkg/api/generated/`, `src/lib/api/generated/`) are owned solely by **U10**. No other unit regenerates or edits them.

### Ownership table

| Unit | Work items | Files owned (exclusive write) | Depends on | Must NOT touch |
|---|---|---|---|---|
| **U1** Named ID types | W20 | NEW `pkg/session/ids.go` | — | any existing file |
| **U2** Strict store API | W3 (store half), W1 (store half) | NEW `pkg/session/unified_api.go` (`AppendTranscriptStrict`, `CreateSessionWithID`) | U1, U4 | `pkg/session/unified.go` — request lock-helper changes from U4 |
| **U3** Turn-local identity | W3 (4 writers), W4 (`turnState.routingSessionID` + 3 resolvers) | `pkg/agent/turn.go` | U1, U2 | `pkg/agent/subturn.go`, `pkg/agent/steering.go`, `pkg/agent/loop.go` |
| **U4** Store striping | W15 | `pkg/session/unified.go` (lock + cache surface), NEW `pkg/session/unified_lock.go` | U1 | `pkg/session/daypartition.go` |
| **U5** meta split + parent fields + predicate deletion | W23, W2 (store half), W11a (delete `IsDelegateChildEntry`) | `pkg/session/unified.go`, `pkg/session/daypartition.go`, NEW `pkg/session/unified_meta_files.go` | **U4** | anything in `pkg/agent`, `pkg/gateway`, `pkg/tools` |
| **U6** Stats throttle | W24 | `pkg/session/unified.go`, NEW `pkg/session/unified_stats_flush.go` | **U5** | `pkg/session/daypartition.go` (U5 is done with it; do not re-edit) |
| **U7** Delegation spawn | W1 (agent half), W4 (subturn half), W10 (Inherit re-key), W21 (SubTurn payload pin) | `pkg/agent/subturn.go` | U2, U3, U5 | `pkg/agent/turn.go`, `pkg/agent/loop.go`, `pkg/security/approvalgrants.go` |
| **U8** Steering + interrupt | W4 (7 predicates), W13 (scope collapse) | `pkg/agent/steering.go` | U3 | `pkg/agent/cancel.go`, `pkg/agent/subturn.go` |
| **U9** Loop payload stamping | W4 (WS payload stamping), W10 (grant read re-key) | `pkg/agent/loop.go` | U3, U10, U17 | `pkg/agent/turn.go`, `pkg/agent/subturn.go` |
| **U10** Contracts + regeneration | W5a | `contracts/**`, `pkg/api/generated/**`, `src/lib/api/generated/**` | — | any hand-written Go/TS consumer |
| **U11** Gateway WS | W3 (streamed write, `pkg/gateway/websocket.go:4256`), W5b (frame stamping), W10 (teardown call site) | `pkg/gateway/websocket.go` | U2, U10, U17 | `pkg/gateway/rest.go`, `pkg/gateway/replay.go`, `pkg/gateway/approvals.go` |
| **U12** SPA | W5c, W16b (sidebar), W19b (drill-down), W2c (SPA enum) | `src/store/chat.ts`, `src/lib/api.ts`, `src/routes/_app/sessions.$sessionId.tsx`, `src/components/**/Sidebar.tsx` | U10 | `src/lib/api/generated/**` |
| **U13** Lifecycle edge + index | W6 | `pkg/session/lifecycle.go`, NEW `pkg/session/lifecycle_index.go` | — | `pkg/session/lifecycle_lock.go` (read-only precedent), `pkg/session/unified.go` |
| **U14** Delegate tool | W7a (refuse), W9a (cancel kills shells), W12 (ancestor walk), W14 (status/leak), W21b (`DelegateTaskState.SessionID`), W6 doc-rot in `list_jobs_sources.go` | `pkg/tools/delegate.go`, `pkg/tools/list_jobs_sources.go` | U13 | `pkg/tools/shell.go`, `pkg/tools/session.go`, `pkg/tools/inspect_session.go` |
| **U15** Cancel orchestration | W8, W4 (pre-arm keys) | `pkg/agent/cancel.go`, `pkg/agent/cancel_prearm.go`, `pkg/agent/orphan_watch.go` | U8, U13, U16 | `pkg/agent/steering.go` |
| **U16** Background shells | W9b | `pkg/tools/shell.go`, `pkg/tools/session.go` | — | `pkg/tools/delegate.go` |
| **U17** Approvals + session teardown | W10 (store, registry, teardown) | `pkg/security/approvalgrants.go`, `pkg/gateway/approvals.go`, `pkg/agent/session_end.go`, `pkg/agent/tool_manifest.go` | — | `pkg/agent/loop.go`, `pkg/agent/subturn.go` |
| **U18** Read boundaries + REST | W11b (4 filter sites), W16a (REST pagination), W19a (drill-down endpoint) | `pkg/gateway/replay.go`, `pkg/gateway/rest.go`, `pkg/agent/verifier_adjudication.go`, `pkg/tools/inspect_session.go` | U5 | `pkg/session/daypartition.go` — **the predicate deletion is U5's line item W11a** |
| **U19** Admission + wiring | W17, W7b (fail-closed wiring) | `pkg/agent/admission.go`, `pkg/agent/session_messaging_wire.go` | U14 | `pkg/agent/subturn.go` |
| **U20** Uploads cascade | W18 | `pkg/tools/normalization.go`, `pkg/media/tempdir.go`, `pkg/media/store.go` | — | `pkg/gateway/rest.go` — request the cascade-delete hook from U18 |
| **U21** Test inversions | W22 | the 12 named `*_test.go` files + any test asserting the old contract | all behaviour units | **any non-test file** |

### Integration order

```
Wave A  (parallel, no interdependencies)
  U1  types          U4  striping        U13 lifecycle index
  U16 shells         U20 uploads         U10 contracts+regen

Wave B  (parallel)                       [needs Wave A]
  U2  strict store API   (needs U1,U4)
  U5  meta split         (needs U4)   ← SERIAL after U4, same file
  U17 approvals
  U14 delegate tool      (needs U13)

Wave C  (parallel)                       [needs Wave B]
  U3  turn.go            (needs U1,U2)
  U6  stats throttle     (needs U5)   ← SERIAL after U5, same file
  U8  steering           (needs U3)   -- may start once U3's turnState field lands
  U18 read boundaries    (needs U5)
  U11 gateway WS         (needs U2,U10,U17)
  U12 SPA                (needs U10)
  U19 admission+wiring   (needs U14)

Wave D  (parallel)                       [needs Wave C]
  U7  subturn            (needs U2,U3,U5)
  U9  loop.go            (needs U3,U10,U17)
  U15 cancel             (needs U8,U13,U16)

Wave E  (own commit, no behaviour files)
  U21 test inversions
```

**Hard orderings (violating any of these is a defect, not a preference):**

1. **U4 → U5 → U6** (`W15 → W23 → W24`). W15 must land before W23 because the split's four targeted writers each take a per-session shard; writing them against the old store-global mutex means four lock acquisitions where there was one — strictly worse than today. W23 must land before W24 because throttling counters that still live in the fused document is **Alternative F**, which is rejected. **Do not land W24 without W23.**
2. **U2 (AC-1's primitive) before any acceptance measurement.** ADR-057 §10: until `AppendTranscript` fails loudly, a green suite is not evidence.
3. **U10 (contracts) before U9, U11, U12.** Constraint #8: schema first, generated types only, one atomic commit.
4. **U5 before U18.** Deleting the four filter sites before the child owns its own file un-hides narration with nothing gained.
5. **U21 last, in its own commit.** Bisection must be able to distinguish "the contract changed" from "the behaviour regressed".

**Cross-unit requests (a unit needing a change it does not own):**

| Requesting unit | Needs | From owner |
|---|---|---|
| U2 | a `lockSession(id)` helper on `UnifiedStore` | U4 |
| U18 | `IsDelegateChildEntry` removed from `daypartition.go` | U5 |
| U20 | the child-uploads cascade hook invoked on session delete | U18 (`pkg/gateway/rest.go`) |
| U14 | `KillBackgroundSessions` reachable from the `delegate cancel` path | U16 |
| U9 / U11 | the `producing_session_id` generated type | U10 |
| U7 | the exported exact-id create wrapper | U2 |
| U15 | the descendant-set accessor computed in PHASE A | U8 |

---

## BDD Scenarios

### Feature: Session parent/child unification (ADR-057)

#### Background

- **Given** a real `UnifiedStore` rooted at a temporary directory on the real filesystem
- **And** a real lifecycle store wired to the delegate tool
- **And** parent and child session ids constructed as **distinct, non-equal** values

---

#### BDD-01 — Scenario: Transcript append to an unknown session fails loudly and creates nothing

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Error Path

- **Given** a real `UnifiedStore` containing no session with id `X`
- **When** `AppendTranscriptStrict(X, entry)` is called
- **Then** a non-nil error is returned
- **And** `os.Stat(<baseDir>/X)` reports the path does not exist
- **But** no WARN-and-return-nil path is taken

#### BDD-02 — Scenario: Transcript append to an existing session appends exactly one line

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a real session `Y` created through the store
- **When** `AppendTranscriptStrict(Y, entry)` is called
- **Then** nil is returned
- **And** `<baseDir>/Y/transcript.jsonl` contains exactly one more line than before

#### BDD-03 — Scenario Outline: Each turn-level transcript writer surfaces an unresolvable session id

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Error Path

- **Given** a registered turn whose transcript store is wired and whose session id does not resolve
- **When** the writer at `<site>` runs
- **Then** an error counter is incremented and a WARN naming the session id is emitted

**Examples**:

| site | writer |
|---|---|
| `pkg/agent/turn.go:1130` | tool-call transcript |
| `pkg/agent/turn.go:1208` | intermediate assistant |
| `pkg/agent/turn.go:1270` | final assistant |
| `pkg/agent/turn.go:1325` | error transcript |
| `pkg/gateway/websocket.go:4256` | streamed assistant |

#### BDD-04 — Scenario: An abandoned turn's suppressed write is counted, not silent

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a registered turn with `ts.abandoned` set
- **When** a transcript write is attempted
- **Then** the write is suppressed
- **And** a counter increments and a log line records the suppression
- **But** the function does not return silently as it does today (`pkg/agent/turn.go:1296-1299`)

#### BDD-05 — Scenario: Routing and session ids are distinct types

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the merged tree compiles
- **When** a `RoutingSessionID` is assigned to a `SessionID` without conversion
- **Then** compilation fails

---

#### BDD-06 — Scenario: A delegation creates a store-backed session with the exact child id

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a parent chat session and a registered parent turn
- **When** one delegation is dispatched
- **Then** `<baseDir>/<childID>/meta.json` exists on disk
- **And** the session id inside it equals `childID` exactly

#### BDD-07 — Scenario: The child inherits the parent's session owner

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a parent session whose `meta.Owner` is a non-empty principal
- **When** a child spawns and executes `system.workspace.create`
- **Then** the created entity's owner is non-empty and equals the parent's owner

#### BDD-08 — Scenario: A child with no inherited owner does not silently disable ownership stamping

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Error Path

- **Given** a parent session whose `meta.Owner` is empty
- **When** a child spawns
- **Then** the absence is observable in logs rather than only manifesting as an unstamped entity later

#### BDD-09 — Scenario: The child's process options carry no `NoHistory` flag

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a delegation about to spawn
- **When** the child's `processOptions` are constructed
- **Then** `NoHistory` is absent
- **And** `TranscriptSessionID` equals `childID`

#### BDD-10 — Scenario: The child's meta names its direct parent and a subordinate type

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a delegation at depth 2 (a grandchild)
- **When** the grandchild's meta is read
- **Then** `ParentSessionID` equals the depth-1 child's id, not the chat's
- **And** the session type is the subordinate value

#### BDD-11 — Scenario Outline: Per-delegation controls take the same single id as today

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** a running child session
- **When** `<action>` is invoked with the child's id
- **Then** it resolves and acts on that child

**Examples**:

| action |
|---|
| `steer` |
| `respond` |
| `cancel` |
| `peek` |
| `inbox` |
| `follow_up` |

---

#### BDD-12 — Scenario: A root turn's routing id equals its own session id

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a root chat turn with session id `S`
- **When** `routingSessionID` is read
- **Then** it equals `S`
- **And** every session-scoped frame it emits carries `session_id == S` with no `producing_session_id`

#### BDD-13 — Scenario: A child inherits the routing id verbatim and the pre-arm latch still matches

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a parent turn that sets a pending-spawn pre-arm marker
- **When** the child spawns and later clears the marker
- **Then** the keys cleared are exactly the keys set
- **And** the child's `routingSessionID` equals the parent's

#### BDD-14 — Scenario: Span and steps land in the same SPA bucket on the live connection

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a real gateway and a real WS client subscribed to chat session `S`
- **When** one delegation runs to completion
- **Then** the SPA store's `S` bucket contains the subagent span **and** its tool-call steps
- **And** `spanByParentCallId` resolves for that span
- **But** `logDiagnostic('chatAttachStepSpanIndexMiss')` never fires

#### BDD-15 — Scenario: Span and steps still correlate after a reconnect

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** a delegation in progress
- **When** the browser reconnects mid-delegation and replay completes
- **Then** the span and its steps are in the same bucket and correlate

#### BDD-16 — Scenario Outline: Every session-scoped frame type round-trips both ids

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a child turn emitting frame type `<frame_type>`
- **When** the frame crosses the wire
- **Then** `session_id` equals the routing key and `producing_session_id` equals the child's own id

**Examples**:

| frame_type |
|---|
| `token` |
| `done` |
| `tool_call_start` |
| `tool_call_result` |
| `subagent_start` |
| `subagent_end` |
| `replay_message` |
| `replay_done` |
| `agent_switched` |
| `task_status_changed` |
| `tool_approval_required` |
| `rate_limit` |
| `media` |
| `session_started` |
| `system_overload` |
| `session_close_ack` |
| `cancel_stage` |
| `goal_status` |
| `loop_status` |

#### BDD-17 — Scenario: A read of the routing id outside the closed consumer set fails the build gate

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path

- **Given** the non-test tree
- **When** the consumer-set test enumerates every read of `routingSessionID`
- **Then** it fails if any read appears outside WS payload stamping, the seven role-B predicates, or the pre-arm keys

---

#### BDD-18 — Scenario: Children-of-X returns direct children only

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** persisted lifecycle records for chat `A`, child `B`, grandchild `D` and sibling `C`
- **When** `List(LifecycleFilter{ParentDurableKey: A})` is called
- **Then** exactly `B` and `C` are returned
- **But** `D` is not

#### BDD-19 — Scenario: The parent index makes the walk cost proportional to descendants

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a store containing many unrelated sessions and one small subtree
- **When** the descendant walk runs over that subtree
- **Then** its file-read count scales with the subtree size, not with the total session count

#### BDD-20 — Scenario: Delegation is refused when no lifecycle store is wired

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Error Path

- **Given** a delegate tool with no lifecycle store
- **When** a delegation is attempted
- **Then** it is refused with an operator-visible error
- **But** no child session is created and no success payload is returned

#### BDD-21 — Scenario: A child minted without a parent agent id is still cancellable

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Edge Case

- **Given** `tools.delegate.require_parent_agent_id=false`
- **When** a child is minted with a blank `ParentAgentID` and a Stop is issued on the chat
- **Then** the child is reached by the `ParentDurableKey` walk and cancelled

#### BDD-22 — Scenario: The three parentage doc comments no longer assert shared keys

**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the merged tree
- **When** `pkg/session/lifecycle.go:225-228`, `:571-575` and `pkg/tools/list_jobs_sources.go:311-315` are read
- **Then** none describes `ParentDurableKey` as shared between a parent and its children

---

#### BDD-23 — Scenario: A Stop hard-aborts a live child in PHASE B

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a registered root turn that finishes gracefully and a registered `Critical:true` child turn that does not
- **When** a real Stop is issued on the chat
- **Then** PHASE B's hard abort fires against the child

#### BDD-24 — Scenario: A Stop detaches a surviving child in PHASE C

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the same tree with the child surviving the hard abort window
- **When** PHASE C's window elapses
- **Then** the detach fires against the child

#### BDD-25 — Scenario: The cancel audit entry names the descendants it reached

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a Stop that reached one child
- **When** the `turn_canceled` audit entry is read
- **Then** `descendants_canceled` is non-empty and contains the child's turn id

#### BDD-26 — Scenario: The orphan watchdog defers while a critical delegate is alive

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Edge Case

- **Given** an orphaned root turn and a live `Critical:true` async delegate
- **When** the ADR-045 watchdog evaluates its fire predicate
- **Then** it does not fire

#### BDD-27 — Scenario: The orphan watchdog fires once the delegate finishes

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the same orphaned root
- **When** the critical delegate completes
- **Then** the watchdog fires and reaps the root

#### BDD-28 — Scenario: A chat Stop kills a child's background shell but not a sibling's

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Happy Path

- **Given** child `B` and sibling `C` each running a real background `bash` process
- **When** a chat-level Stop is issued on `B`'s chat with `C` under a different chat
- **Then** `B`'s real PID is gone
- **But** `C`'s process is still alive

#### BDD-29 — Scenario: `delegate action=cancel` kills that child's background shells

**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Happy Path

- **Given** child `B` running a real background `bash` process
- **When** `delegate action=cancel` targets `B`
- **Then** `B`'s real PID is gone

#### BDD-30 — Scenario: Every descendant's lifecycle record transitions to cancelled

**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Happy Path

- **Given** a chat with children at depths 1, 2 and 3
- **When** a Stop is issued on the chat
- **Then** each descendant's persisted lifecycle record reads `cancelled`

---

#### BDD-31 — Scenario: A child executes a parent-granted tool with no prompt

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a standing approval grant on the parent for tool `T`
- **When** a delegated child executes `T`
- **Then** the tool runs immediately
- **But** no approval prompt is raised and no wait occurs

#### BDD-32 — Scenario: A child's inherited grant does not outlive the child

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a child holding an inherited grant keyed to its own session
- **When** the child session terminates
- **Then** the grant set for that session no longer exists

#### BDD-33 — Scenario: A chat Stop cancels a pending approval inside a child

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a pending approval request raised inside a child
- **When** a chat-level Stop is issued
- **Then** the registry entry is gone, its timer is stopped, and the child's goroutine unblocks

#### BDD-34 — Scenario: Child teardown evicts grants, loaded tools and recall spans

**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a child that loaded tools and recorded recall spans
- **When** the child turn reaches a terminal state
- **Then** its grant set, `loadedTools` bucket and `recallSpans` entries are all gone

---

#### BDD-35 — Scenario: The delegate-child predicate has no non-test references

**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the merged tree
- **When** a repo-wide reference check runs for `IsDelegateChildEntry`
- **Then** zero references exist outside tests
- **And** none of the four read boundaries filters on `ParentSpawnCallID`

#### BDD-36 — Scenario: The parent's transcript file contains no child entry

**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Happy Path

- **Given** one completed delegation
- **When** the parent session's `transcript.jsonl` is read directly from disk
- **Then** it contains no entry produced by the child

#### BDD-37 — Scenario Outline: Each read boundary returns the child's transcript unfiltered

**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a completed child session with narration, a final report and a tool call
- **When** `<boundary>` reads that session
- **Then** all of its entries are returned

**Examples**:

| boundary |
|---|
| `GET /api/v1/sessions/{childID}` |
| `GET /api/v1/sessions/{childID}/messages` |
| `inspect_session` |
| live-reconnect replay |

#### BDD-38 — Scenario: Child entries retain spawn-call provenance

**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a child session's own transcript entries
- **When** they are read
- **Then** `ParentSpawnCallID` is populated on the entries that carried it before
- **And** the drill-down surface reads it

#### BDD-39 — Scenario: The verifier window sees only the adjudicated session's entries

**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Happy Path

- **Given** a session under adjudication that spawned a delegation
- **When** the verifier renders its window
- **Then** it receives that session's own entries and nothing else

#### BDD-40 — Scenario: A pre-cutover session shows previously-hidden delegate narration

**Traces to**: User Story 7, Acceptance Scenario 6
**Category**: Edge Case

- **Given** a session file written before the cutover containing entries with `ParentSpawnCallID` set
- **When** it is rendered
- **Then** those entries appear as top-level bubbles
- **And** this is recorded as the accepted, bounded consequence R-16

---

#### BDD-41 — Scenario: A sibling cannot address another sibling

**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Error Path

- **Given** chat `A` with children `B` and `C`
- **When** `B` invokes a gated delegate action against `C`
- **Then** the action is rejected with an ownership error

#### BDD-42 — Scenario: The root chat can still address a grandchild

**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Happy Path

- **Given** chat `A`, child `B` and grandchild `D`
- **When** `A` invokes a gated delegate action against `D`
- **Then** the action is permitted

#### BDD-43 — Scenario: The ancestor walk terminates at the configured depth bound

**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a chain longer than the configured max delegation depth
- **When** the walk runs from the deepest record
- **Then** it stops at the bound and rejects
- **But** it does not loop or scan the whole store

#### BDD-44 — Scenario Outline: Every gated action uses the walk

**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Happy Path

- **Given** chat `A`, child `B` and grandchild `D`
- **When** `A` invokes `<action>` against `D`
- **Then** it is permitted, and when `B`'s sibling invokes the same action it is rejected

**Examples**:

| action | site |
|---|---|
| `inbox` | `pkg/tools/delegate.go:2010` |
| `steer` | `:2107` |
| `respond` | `:2159` |
| `cancel` | `:2321` |
| `follow_up` | `:2459` |
| `peek` | `:2592` |

---

#### BDD-45 — Scenario: An interrupt without an explicit scope does not compile

**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Error Path

- **Given** the collapsed interrupt API
- **When** a caller omits the `InterruptScope` argument
- **Then** compilation fails

#### BDD-46 — Scenario: Subtree-scoped interrupt at a child spares parent and sibling

**Traces to**: User Story 9, Acceptance Scenario 2
**Category**: Happy Path

- **Given** chat `A` with children `B` and `C`, and `B` with child `D`
- **When** `Interrupt(B, ScopeSubtree)` runs
- **Then** `B` and `D` are cancelled
- **But** `A` and `C` keep running

#### BDD-47 — Scenario: Subtree-scoped interrupt at the chat reaches all depths

**Traces to**: User Story 9, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the same tree
- **When** `Interrupt(A, ScopeSubtree)` runs
- **Then** `A`, `B`, `C` and `D` are all cancelled

#### BDD-48 — Scenario: The two-namespace gate test asserts the new invariant

**Traces to**: User Story 9, Acceptance Scenario 4
**Category**: Happy Path

- **Given** `pkg/agent/interrupt_by_session_key_test.go`
- **When** the change lands
- **Then** the test exists and asserts the scoped-interrupt invariant
- **But** it has not been deleted

---

#### BDD-49 — Scenario: A synchronous delegation reports a non-empty activity snapshot

**Traces to**: User Story 10, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a completed synchronous delegation
- **When** `delegate action=status` is called for it
- **Then** the activity snapshot is non-empty

#### BDD-50 — Scenario: An asynchronous delegation reports a non-empty activity snapshot

**Traces to**: User Story 10, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a completed asynchronous delegation
- **When** `delegate action=status` is called for it
- **Then** the activity snapshot is non-empty

#### BDD-51 — Scenario: A genuinely empty activity path logs

**Traces to**: User Story 10, Acceptance Scenario 3
**Category**: Error Path

- **Given** a delegation whose session has no recent activity lines
- **When** `recentActivityLines` returns nothing
- **Then** a log line records the empty result

#### BDD-52 — Scenario: Delegate task maps do not grow without bound

**Traces to**: User Story 10, Acceptance Scenario 4
**Category**: Edge Case

- **Given** N delegations completed and reaped
- **When** the delegate tool's internal maps are inspected
- **Then** they do not retain all N entries

---

#### BDD-53 — Scenario: Concurrent writes to different sessions do not serialise

**Traces to**: User Story 11, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a real on-disk store and N goroutines each creating and appending to its own session
- **When** the run completes at N and again at 2N
- **Then** wall-clock at 2N is materially less than double the wall-clock at N
- **And** the same measurement against the pre-change store is the baseline this must beat

#### BDD-54 — Scenario: Listing does not block on an unrelated in-flight create

**Traces to**: User Story 11, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a `NewSession` in flight on session `A`
- **When** `ListSessions` is called
- **Then** it returns without waiting for `A`'s fsyncs

#### BDD-55 — Scenario: A session create does not stall another session's token stream

**Traces to**: User Story 11, Acceptance Scenario 3
**Category**: Happy Path

- **Given** session `A` streaming transcript appends continuously
- **When** session `B` is created
- **Then** `A`'s inter-token interval stays within its pre-change distribution

#### BDD-56 — Scenario: Mixed concurrent store operations are race-clean

**Traces to**: User Story 11, Acceptance Scenario 4
**Category**: Edge Case

- **Given** concurrent create, append, `SetMeta`, `ListSessions` and `DeleteSession` on overlapping and disjoint ids
- **When** the suite runs under `-race`
- **Then** the run is clean
- **And** `ClearAll` / `RetentionSweep` interleaved with per-session writes neither deadlock nor drop a session

#### BDD-57 — Scenario: No cache critical section performs filesystem work

**Traces to**: User Story 11, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the merged tree
- **When** every `cacheMu` critical section is inspected
- **Then** none contains an `os.*` or `fileutil.*` call
- **And** no code path takes `cacheMu` before a session shard

---

#### BDD-58 — Scenario: A fully exercised session directory holds exactly four meta files

**Traces to**: User Story 12, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a session created, then given one `/goal set`, one `/loop` start and one transcript append
- **When** the session directory is listed
- **Then** `meta.json`, `stats.json`, `goal.json` and `loop.json` all exist
- **And** each file contains only its own group's fields

#### BDD-59 — Scenario Outline: Each writer family leaves the other families' bytes untouched

**Traces to**: User Story 12, Acceptance Scenario 2
**Category**: Happy Path

- **Given** all four files present with known contents
- **When** `<operation>` runs
- **Then** `<unchanged_files>` are byte-identical afterwards

**Examples**:

| operation | unchanged_files |
|---|---|
| `/loop` tick | `goal.json`, `meta.json` |
| `/goal` judge round | `loop.json`, `meta.json` |
| transcript append | `goal.json`, `loop.json` |
| status transition | `goal.json`, `loop.json`, `stats.json` |

#### BDD-60 — Scenario: A directory with only `meta.json` loads with zero-valued groups

**Traces to**: User Story 12, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a session directory containing `meta.json` and nothing else
- **When** it is loaded
- **Then** the load succeeds with zero-valued stats, goal and loop

#### BDD-61 — Scenario: A directory with no `meta.json` is an error, not an empty session

**Traces to**: User Story 12, Acceptance Scenario 4
**Category**: Error Path

- **Given** a session directory containing `stats.json` but no `meta.json`
- **When** it is loaded
- **Then** `readUnifiedMeta` returns an error
- **And** `GET /api/v1/sessions/{id}` returns 404

#### BDD-62 — Scenario: A corrupt group file surfaces an error for that group

**Traces to**: User Story 12, Acceptance Scenario 5
**Category**: Error Path

- **Given** a session directory with a present but truncated `goal.json`
- **When** it is loaded
- **Then** an error surfaces for the goal group
- **But** the load does not silently compose a zero-valued goal

#### BDD-63 — Scenario: The wire representation is byte-identical across the split

**Traces to**: User Story 12, Acceptance Scenario 6
**Category**: Happy Path

- **Given** the same logical session state before and after the split
- **When** `UnifiedMeta` is marshalled and the REST session payload is rendered
- **Then** the bytes are identical
- **And** `make verify-contracts` exits 0 with no regeneration

#### BDD-64 — Scenario: The write-funnel doc comments no longer claim a single funnel

**Traces to**: User Story 12, Acceptance Scenario 7
**Category**: Happy Path

- **Given** the merged tree
- **When** `pkg/session/unified.go:780-785` and `:166-181` are read
- **Then** neither asserts that every mutation path funnels through one whole-document write

---

#### BDD-65 — Scenario: A burst of appends does not touch the stats file

**Traces to**: User Story 13, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a session and a flush interval that has just started
- **When** K transcript appends occur inside that interval
- **Then** `stats.json`'s mtime and bytes are unchanged
- **And** `transcript.jsonl` has exactly K new lines

#### BDD-66 — Scenario: The stats file matches the counters exactly after the interval

**Traces to**: User Story 13, Acceptance Scenario 2
**Category**: Happy Path

- **Given** K appends with known token, cost and tool-call deltas
- **When** the flush interval elapses
- **Then** `stats.json` on disk equals the sum of those deltas exactly

#### BDD-67 — Scenario Outline: Each forced flush point leaves the stats file current

**Traces to**: User Story 13, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** pending in-memory counter deltas and no elapsed flush interval
- **When** `<flush_point>` fires
- **Then** `stats.json` is current, and re-opening the store reads back the exact counters

**Examples**:

| flush_point |
|---|
| `SetMeta` carrying a `Status` patch |
| `DeleteSession` |
| `UnifiedStore.Close` |
| child `CloseSession` teardown |

#### BDD-68 — Scenario Outline: Event-driven writes are on disk immediately

**Traces to**: User Story 13, Acceptance Scenario 4
**Category**: Happy Path

- **Given** no flush interval has elapsed
- **When** `<event>` completes
- **Then** `<field>` is readable from disk immediately

**Examples**:

| event | field | file |
|---|---|---|
| `/goal` judge round | `GoalRoundsUsed` | `goal.json` |
| `/loop` tick | `LoopRunCount` | `loop.json` |
| status transition | `Status` | `meta.json` |
| title change | `Title` | `meta.json` |

#### BDD-69 — Scenario: Recency ordering is exact within a live process

**Traces to**: User Story 13, Acceptance Scenario 5
**Category**: Happy Path

- **Given** session `A` streamed, then session `B` streamed, with no flush in between
- **When** `ListSessions` is called
- **Then** `B` sorts ahead of `A`

#### BDD-70 — Scenario: An ungraceful kill loses at most one interval of counters

**Traces to**: User Story 13, Acceptance Scenario 6
**Category**: Edge Case

- **Given** a session mid-interval with pending counter deltas
- **When** the process is killed without a graceful shutdown and the store is re-opened
- **Then** the counters are behind by at most that interval's appends
- **And** `transcript.jsonl` is complete

---

#### BDD-71 — Scenario: A hidden delegation is inspectable without verbose chat

**Traces to**: User Story 14, Acceptance Scenario 1
**Category**: Happy Path

- **Given** verbose chat disabled and a completed delegation hidden from the thread
- **When** the drill-down surface is opened by child id
- **Then** the child's transcript is displayed using only `GET /api/v1/sessions/{childID}`

#### BDD-72 — Scenario: The session list paginates

**Traces to**: User Story 14, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a store with more sessions than one page
- **When** `GET /api/v1/sessions` is called with paging parameters
- **Then** a bounded page is returned with a means to fetch the next

#### BDD-73 — Scenario: A wide fan-out does not evict the parent chat from the sidebar

**Traces to**: User Story 14, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a parent chat with 24 child sessions created after it
- **When** the sidebar renders
- **Then** the parent chat is still visible

#### BDD-74 — Scenario: Drill-down filters on the producing session

**Traces to**: User Story 14, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the drill-down view for a child
- **When** it filters frames
- **Then** it filters on `producing_session_id`
- **But** it does not depend on `subagent_message` or `subagent_state`

---

#### BDD-75 — Scenario: The root-delegation cap refuses rather than queues

**Traces to**: User Story 15, Acceptance Scenario 1
**Category**: Error Path

- **Given** a configured root-level delegation cap of N with N in flight
- **When** the N+1th root-level delegation is attempted
- **Then** it is refused
- **But** it is not queued behind the session-store lock

#### BDD-76 — Scenario: Nested delegation gating is unchanged

**Traces to**: User Story 15, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the root-level gate in effect
- **When** a child-level delegation runs
- **Then** its existing `concurrencySem` behaviour is unchanged

#### BDD-77 — Scenario: The refusal is operator-visible

**Traces to**: User Story 15, Acceptance Scenario 3
**Category**: Error Path

- **Given** a refused root-level delegation
- **When** the refusal surfaces
- **Then** it names the cap and is visible to the operator

---

#### BDD-78 — Scenario: Deleting a parent removes every descendant's uploads directory

**Traces to**: User Story 16, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a parent session with descendants at depths 1 and 2 that each uploaded media
- **When** the parent session is deleted
- **Then** `<home>/uploads/<id>/` is gone for every descendant

#### BDD-79 — Scenario: A path-unsafe session id is still rejected

**Traces to**: User Story 16, Acceptance Scenario 2
**Category**: Error Path

- **Given** a session id containing `..` or a path separator
- **When** the uploads directory is resolved
- **Then** the resolver returns no directory and the caller falls back to the ephemeral temp dir

---

#### BDD-80 — Scenario: Every named gate test asserts the new invariant

**Traces to**: User Story 17, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the twelve gate test files named by W22
- **When** the change lands
- **Then** each exists and asserts the new invariant
- **But** none has been deleted and none still asserts the old one

#### BDD-81 — Scenario: Test inversions land in their own commit

**Traces to**: User Story 17, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the commit history for this change
- **When** the W22 commit is inspected
- **Then** it contains only `*_test.go` files

#### BDD-82 — Scenario: Every test uses distinct parent and child ids

**Traces to**: User Story 17, Acceptance Scenario 3
**Category**: Edge Case

- **Given** any test written or inverted for this spec
- **When** it constructs the parent and child session ids
- **Then** the two values are not equal
- **And** the assertion names which of the two was used

---

#### BDD-83 — Scenario: `follow_up` generation N+1 sees generation N's history

**Traces to**: User Story 18, Acceptance Scenario 1
**Category**: Alternate Path

- **Given** a completed child session that produced assistant output in generation N
- **When** `follow_up` resumes the same `childID` for generation N+1
- **Then** generation N's messages appear in generation N+1's first assembled message list

#### BDD-84 — Scenario: The per-child message ceiling is enforced per direct parent

**Traces to**: User Story 18, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a depth-3 tree where each parent has C children
- **When** each child sends messages up to the per-child ceiling
- **Then** each direct-parent relationship enforces the ceiling independently
- **And** the chat's aggregate equals (C × ceiling), not one shared pool

#### BDD-85 — Scenario: A grandchild's `message_parent` is drained only by its direct parent

**Traces to**: User Story 18, Acceptance Scenario 3
**Category**: Happy Path

- **Given** chat `A`, child `B` and grandchild `D`
- **When** `D` calls `message_parent`
- **Then** `B`'s `delegate action=inbox` drains the message
- **But** `A`'s `delegate action=inbox` does not, and does not return a clean empty success in place of it

#### BDD-86 — Scenario: A 3P child's process group dies with the child

**Traces to**: User Story 18, Acceptance Scenario 4
**Category**: Edge Case

- **Given** an external-CLI child that has spawned its own subprocess tree
- **When** the child is cancelled
- **Then** every PID in the child's process group is gone

#### BDD-87 — Scenario: A restart mid-delegation leaves no orphan directory

**Traces to**: User Story 18, Acceptance Scenario 5
**Category**: Edge Case

- **Given** a parent turn mid-delegation with a persisted child lifecycle record
- **When** the process restarts and the boot sweep runs
- **Then** the child's lifecycle record is reconciled to a terminal state
- **And** no transcript write lands in a directory with no `meta.json`

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| **Unit** | One function or type against a real `UnifiedStore` / real lifecycle store on `t.TempDir()` | Validates the primitive in isolation. **No spies, no fakes** — the store is real, the files are real |
| **Integration** | Two or more subsystems in one process: a registered turn + a real store; the cancel ladder + real turns; the gateway + a real WS client | Validates that the id actually threaded through, rather than that a function was called |
| **Cross-process** | The test binary re-exec'd as real OS processes, copying `pkg/entity/store_crossprocess_test.go` | The only honest way to assert a durability or lock guarantee that spans processes |
| **E2E** | Real gateway binary + real SPA store + real delegation | Validates the user-visible property (bucket membership, drill-down, sidebar) |

**Build tags are mandatory.** Every Go invocation carries `-tags goolm,stdjson` (prefer `make test`). Without them `pkg/channels/matrix` will not compile and the gateway package fails to build — a missing-tag error that reads like a real failure but is not.

**Do not run the full Go suite in the dev pod.** CI is the authority. At most one narrowly scoped local test: `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/...`. The full suite (especially `pkg/gateway`) OOM-kills this environment.

### Test Implementation Order

Write these before the implementation code. Order is by dependency; the Unit tier for a unit's own primitive comes before any Integration test that consumes it.

| # | Test name | Level | Unit | Traces to BDD | What it verifies |
|---|---|---|---|---|---|
| 1 | `TestAppendTranscriptStrict_UnknownSession_ErrorsAndCreatesNothing` | Unit | U2 | BDD-01 | Non-nil error **and** `os.Stat` IsNotExist on the would-be dir |
| 2 | `TestAppendTranscriptStrict_KnownSession_AppendsExactlyOneLine` | Unit | U2 | BDD-02 | Line count delta of exactly 1 |
| 3 | `TestSessionIDTypes_DoNotInterconvert` | Unit | U1 | BDD-05 | Compile-fail fixture / `go vet`-style gate |
| 4 | `TestCreateSessionWithID_UsesExactIDAndCopiesOwner` | Unit | U2 | BDD-06, BDD-07 | Directory name == supplied id; `meta.Owner` copied |
| 5 | `TestTurnTranscriptWriters_SurfaceUnresolvableSession` | Unit | U3 | BDD-03 | 4 writers → counter + WARN each |
| 6 | `TestWebsocketStreamedWrite_SurfacesUnresolvableSession` | Unit | U11 | BDD-03 | `pkg/gateway/websocket.go:4256` |
| 7 | `TestAbandonedTurn_SuppressedWriteIsCounted` | Unit | U3 | BDD-04 | Suppression emits a signal |
| 8 | `TestStripedSessionLock_ShardIsolation` | Unit | U4 | BDD-57 | Distinct ids map to distinct shards; `Get` is stable per key |
| 9 | `TestCacheMu_NoFilesystemInCriticalSection` | Unit | U4 | BDD-57 | Static/AST gate over `cacheMu` regions |
| 10 | `TestLifecycleFilter_ParentDurableKey_DirectChildrenOnly` | Unit | U13 | BDD-18 | Depth-1 only; grandchild excluded |
| 11 | `TestLifecycleParentIndex_MaintainedInsidePersist` | Unit | U13 | BDD-19 | Index updated under the striped lock; one file read per query |
| 12 | `TestLifecycleDocComments_NoSharedParentChildClaim` | Unit | U13, U14 | BDD-22 | Doc-truth gate over the three comment blocks |
| 13 | `TestReadUnifiedMeta_ComposesFourFiles` | Unit | U5 | BDD-58 | All four read and composed |
| 14 | `TestReadUnifiedMeta_MissingGroupFilesAreZeroValue` | Unit | U5 | BDD-60 | Success with zero stats/goal/loop |
| 15 | `TestReadUnifiedMeta_MissingMetaJSONIsError` | Unit | U5 | BDD-61 | Error, not empty session — asymmetry asserted in both directions |
| 16 | `TestReadUnifiedMeta_CorruptGroupFileErrors` | Unit | U5 | BDD-62 | Truncated `goal.json` → error for that group |
| 17 | `TestMetaWriters_WriterIsolationByteLevel` | Unit | U5 | BDD-59 | Each op leaves the other files' bytes identical |
| 18 | `TestUnifiedMetaMarshal_ByteIdenticalAcrossSplit` | Unit | U5 | BDD-63 | Golden-bytes comparison |
| 19 | `TestMetaDocComments_NoSingleFunnelClaim` | Unit | U5 | BDD-64 | Doc-truth gate |
| 20 | `TestStatsThrottle_NoFileWriteWithinInterval` | Unit | U6 | BDD-65 | `stats.json` mtime + bytes unchanged; transcript grows |
| 21 | `TestStatsThrottle_ExactCountersAfterInterval` | Unit | U6 | BDD-66 | Sum equality, no lost/double delta |
| 22 | `TestStatsThrottle_ForcedFlushPoints` | Unit | U6 | BDD-67 | 4 flush points, each independently |
| 23 | `TestEventWrites_NotThrottled` | Unit | U6 | BDD-68 | goal/loop/status/title on disk immediately |
| 24 | `TestListSessions_RecencyExactInProcess` | Unit | U6 | BDD-69 | In-memory `UpdatedAt` bump orders correctly |
| 25 | `TestApprovalGrants_InheritKeyedToChildSession` | Unit | U17 | BDD-31, BDD-32 | Grant resolves under the child key, and only there |
| 26 | `TestSessionUploadsDir_RejectsUnsafeID` | Unit | U20 | BDD-79 | Existing `("", false)` contract preserved |
| 27 | `TestInterruptScope_RequiredByCompiler` | Unit | U8 | BDD-45 | Compile-fail fixture |
| 28 | `TestRoutingSessionID_RootEqualsOwnSessionID` | Unit | U3 | BDD-12 | Root behaviour byte-identical |
| 29 | `TestRoutingSessionID_ConsumerSetIsClosed` | Unit | U3 | BDD-17 | Enumerates non-test reads; fails on any outside the set |
| 30 | `TestRecentActivityLines_LogsEmptyPath` | Unit | U14 | BDD-51 | Empty path logged |
| 31 | `TestDelegateTaskMaps_HaveDeletionPath` | Unit | U14 | BDD-52 | Maps shrink after reap |
| 32 | `TestSpawnSubTurn_ChildOwnsRealSession` | Integration | U7 | BDD-06 | `meta.json` exists at `<baseDir>/<childID>` after a real spawn |
| 33 | `TestSpawnSubTurn_NoHistoryFlagRemoved` | Integration | U7 | BDD-09 | Options carry no `NoHistory`; `TranscriptSessionID == childID` |
| 34 | `TestSpawnSubTurn_OwnerInheritedAndInstalled` | Integration | U7, U9 | BDD-07, BDD-08 | `WithSessionOwner` installs; entity stamped with parent's owner |
| 35 | `TestChildMeta_ParentSessionIDAndSubordinateType` | Integration | U5, U7 | BDD-10 | Depth-2 names depth-1, not the chat |
| 36 | `TestDelegateControls_ResolveByChildID` | Integration | U7, U14 | BDD-11 | All six actions resolve on the same id |
| 37 | `TestPreArmLatch_KeysSetAndClearedMatch` | Integration | U15 | BDD-13 | Same keys set and cleared; child consumes a pre-arrival Stop |
| 38 | `TestCancel_PhaseB_HardAbortsLiveChild` | Integration | U15 | BDD-23 | Real root + real critical child + real Stop |
| 39 | `TestCancel_PhaseC_DetachesSurvivingChild` | Integration | U15 | BDD-24 | Detach fires |
| 40 | `TestCancel_AuditNamesDescendants` | Integration | U15 | BDD-25 | `descendants_canceled` non-empty and names the child |
| 41 | `TestOrphanWatchdog_DefersWhileCriticalDelegateAlive` | Integration | U15 | BDD-26 | Fire predicate condition 2 holds |
| 42 | `TestOrphanWatchdog_FiresAfterDelegateFinishes` | Integration | U15 | BDD-27 | Reaps once clear |
| 43 | `TestCancel_KillsChildShellsNotSiblings` | Integration | U15, U16 | BDD-28 | Real PIDs; sibling survives |
| 44 | `TestDelegateCancel_KillsThatChildsShells` | Integration | U14, U16 | BDD-29 | Real PID gone via the delegate path |
| 45 | `TestCancel_TransitionsEveryDescendantLifecycleRecord` | Integration | U15, U13 | BDD-30 | Depth 3, all records `cancelled` |
| 46 | `TestPendingApproval_CancelledByChatStop` | Integration | U17, U11 | BDD-33 | Registry entry gone, timer stopped, goroutine unblocked |
| 47 | `TestChildCloseSession_EvictsGrantsToolsAndRecallSpans` | Integration | U17 | BDD-34 | All three evicted |
| 48 | `TestDelegationRefusedWithoutLifecycleStore` | Integration | U14, U19 | BDD-20 | Operator-visible refusal, no child session created |
| 49 | `TestChildReachableWithBlankParentAgentID` | Integration | U13, U14 | BDD-21 | Walk still reaches it; Stop cancels it |
| 50 | `TestOwnershipWalk_SiblingRejectedAncestorAllowed` | Integration | U14 | BDD-41, BDD-42 | Sibling rejected, root-over-grandchild allowed |
| 51 | `TestOwnershipWalk_DepthBounded` | Integration | U14 | BDD-43 | Terminates at the bound |
| 52 | `TestOwnershipWalk_AllSixGatedActions` | Integration | U14 | BDD-44 | Six sites, both directions |
| 53 | `TestInterrupt_SubtreeAtChildSparesParentAndSibling` | Integration | U8 | BDD-46 | Inverted `interrupt_by_session_key_test.go` assertion |
| 54 | `TestInterrupt_SubtreeAtChatReachesAllDepths` | Integration | U8 | BDD-47 | Three depths |
| 55 | `TestDelegateStatus_SyncAndAsyncSnapshotsNonEmpty` | Integration | U14 | BDD-49, BDD-50 | `executeSync` now registers state |
| 56 | `TestParentTranscriptContainsNoChildEntry` | Integration | U7, U18 | BDD-36 | **Asserted on the file**, so a re-added filter cannot satisfy it |
| 57 | `TestReadBoundaries_ReturnChildTranscriptUnfiltered` | Integration | U18 | BDD-37 | All four boundaries |
| 58 | `TestIsDelegateChildEntry_ZeroNonTestReferences` | Integration | U5, U18 | BDD-35 | Repo-wide reference gate |
| 59 | `TestChildEntries_RetainParentSpawnCallID` | Integration | U7, U18 | BDD-38 | Provenance retained with a named reader |
| 60 | `TestVerifierWindow_OwnSessionEntriesOnly` | Integration | U18 | BDD-39 | Adjudicated session only |
| 61 | `TestPreCutoverSession_ShowsPreviouslyHiddenNarration` | Integration | U18 | BDD-40 | R-16 asserted as the accepted outcome, not as a bug |
| 62 | `TestUploadsCascadeDeleteAcrossDescendants` | Integration | U20, U18 | BDD-78 | Depths 1 and 2 both removed |
| 63 | `TestRootDelegationAdmission_RefusesNotQueues` | Integration | U19 | BDD-75, BDD-77 | N+1 refused, operator-visible |
| 64 | `TestNestedDelegationGating_Unchanged` | Integration | U19 | BDD-76 | `concurrencySem` behaviour preserved |
| 65 | `TestBootSweep_ReconcilesChildAcrossRestart` | Integration | U13, U19 | BDD-87 | No orphan-directory write after restart (AC-19) |
| 66 | `TestMessageParent_DrainedByDirectParentAtDepth3` | Integration | U14 | BDD-85 | Producer and consumer agree (AC-16) |
| 67 | `TestPerChildMessageCeiling_IsPerDirectParent` | Integration | U14 | BDD-84 | Aggregate is (children × ceiling), asserted (AC-15) |
| 68 | `TestFollowUpResume_SeesPreviousGeneration` | Integration | U7 | BDD-83 | Intended behaviour pinned (AC-11) |
| 68a | `TestExternalCLIChild_ProcessGroupDies` | Integration | U14, U16 | BDD-86 | Real PIDs in the child's process group, all gone (AC-17c) |
| 69 | `TestCrossProcess_ConcurrentSessionWritesDoNotLoseUpdates` | Cross-process | U4 | BDD-53 | Re-execs the binary as real OS processes |
| 70 | `TestStoreSharding_SlopeNotDoubling` | Cross-process | U4 | BDD-53 | Asserts the **slope** at N and 2N against a pre-change baseline; no machine constant |
| 71 | `TestListSessions_DoesNotBlockOnUnrelatedCreate` | Integration | U4 | BDD-54 | Real fsyncs in flight |
| 72 | `TestStreamingUnaffectedByForeignSessionCreate` | Integration | U4 | BDD-55 | Inter-token distribution preserved |
| 73 | `TestStoreConcurrency_RaceClean` | Integration | U4 | BDD-56 | `-race`; `ClearAll`/`RetentionSweep` interleaved |
| 74 | `TestStatsThrottle_UngracefulKillBoundedLoss` | Cross-process | U6 | BDD-70 | Real SIGKILL, real re-open |
| 75 | `TestFrameContract_BothIDsRoundTrip` | E2E | U10, U11, U12 | BDD-16 | All 19 session-scoped types |
| 76 | `TestDelegationSpanAndStepsShareBucket_LiveConnection` | E2E | U11, U12 | BDD-14 | Real gateway; SPA store bucket membership; miss diagnostic never fires |
| 77 | `TestDelegationSpanAndStepsShareBucket_AfterReconnect` | E2E | U11, U12 | BDD-15 | Reconnect case |
| 78 | `TestDrillDownReachableWithoutVerboseChat` | E2E | U12, U18 | BDD-71, BDD-74 | Only `GET /api/v1/sessions/{childID}` |
| 79 | `TestSessionListPaginates` | E2E | U12, U18 | BDD-72 | All four layers |
| 80 | `TestSidebarRetainsParentUnderWideFanOut` | E2E | U12 | BDD-73 | 24 children, parent still shown |
| 81 | `TestGateTestsInvertedNotDeleted` | Unit | U21 | BDD-80 | All twelve files present and asserting the new invariant |
| 82 | `TestW22CommitContainsOnlyTests` | Unit | U21 | BDD-81 | Commit-shape gate |
| 83 | `TestAllFixturesUseDistinctParentChildIDs` | Unit | U21 | BDD-82 | Closes the `message_parent_real_context_test.go:16-17` hole |

### Test Datasets

#### Dataset: `AppendTranscriptStrict` session-id resolution

| # | Input session id | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `""` | Empty | Error from `validateSessionID` (pre-existing, `pkg/session/unified.go:803-805`) | BDD-01 | Already loud today; must stay loud |
| 2 | Fresh UUID, never created | Missing entity | Non-nil error **and no directory created** | BDD-01 | The core R-7 case |
| 3 | Existing session id | Valid representative | nil; one new line | BDD-02 | Happy path |
| 4 | Existing session **directory** with no `meta.json` | Corrupted state | Non-nil error | BDD-01, BDD-61 | The D11 asymmetry meets W3 |
| 5 | `"../escape"` | Injection | Error from `validateSessionID` | BDD-01 | Path traversal |
| 6 | `".hidden"` | Special name | Error from `validateSessionID` | BDD-01 | Leading-dot reject |
| 7 | Id of a session deleted between resolve and append | Race: create/delete | Non-nil error, no re-creation | BDD-01 | Must not resurrect the directory |

#### Dataset: `readUnifiedMeta` file-composition matrix

| # | Files present | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | all four | Valid representative | Composed meta, all groups populated | BDD-58 | Happy path |
| 2 | `meta.json` only | Min | Success; stats/goal/loop zero-valued | BDD-60 | A session that never ran a goal |
| 3 | `meta.json` + `stats.json` | Partial | Success; goal/loop zero-valued | BDD-60 | Common streaming session |
| 4 | no `meta.json`, others present | Missing required | **Error**; REST 404 | BDD-61 | Inverting this re-opens R-7 |
| 5 | none | Empty | Error | BDD-61 | "This session does not exist" |
| 6 | all four, `goal.json` truncated mid-object | Corrupted payload | Error for the goal group | BDD-62 | Not a silent zero goal |
| 7 | all four, `stats.json` truncated | Corrupted payload | Error for the stats group | BDD-62 | Same rule, different group |
| 8 | all four, `loop.json` = `{}` | Empty object | Success; zero-valued loop | BDD-60 | Valid empty vs corrupt is distinguishable |
| 9 | `meta.json` + a stale extra file | Unexpected content | Success; extra ignored | BDD-58 | Forward tolerance |

#### Dataset: `UpdatedAt` composition and recency

| # | `meta.json` UpdatedAt | `stats.json` UpdatedAt | Boundary type | Expected composed value | Traces to |
|---|---|---|---|---|---|
| 1 | `T` | absent | Missing group | `T` | BDD-60 |
| 2 | `T` | `T+5s` | Later in stats | `T+5s` | BDD-69 |
| 3 | `T+5s` | `T` | Later in meta | `T+5s` | BDD-69 |
| 4 | `T` | `T` | Equal | `T` | BDD-69 |
| 5 | zero time | `T` | Epoch / zero | `T` | BDD-69 |

#### Dataset: Ownership-walk topology

| # | Caller | Target | Boundary type | Expected | Traces to |
|---|---|---|---|---|---|
| 1 | chat `A` | child `B` | Direct parent | Permit | BDD-44 |
| 2 | chat `A` | grandchild `D` | Ancestor, depth 2 | Permit | BDD-42 |
| 3 | child `B` | sibling `C` | Sibling | **Reject** | BDD-41 |
| 4 | grandchild `D` | cousin `E` | Cousin | **Reject** | BDD-41 |
| 5 | child `B` | own child `D` | Direct parent | Permit | BDD-44 |
| 6 | child `D` | ancestor `A` | Inverted direction | **Reject** | BDD-41 |
| 7 | node at depth `maxDepth+1` | root | Max + 1 | **Reject** at the bound | BDD-43 |
| 8 | caller with empty owner key | any | Empty | **Reject** (existing behaviour, `pkg/tools/delegate.go:1975`) | BDD-41 |
| 9 | target with empty `ParentDurableKey` | — | Empty | **Reject** (existing behaviour) | BDD-41 |

#### Dataset: Stats-throttle timing

| # | Appends | Elapsed vs flush interval | Boundary type | Expected `stats.json` | Traces to |
|---|---|---|---|---|---|
| 1 | 0 | 0 | Zero | unchanged | BDD-65 |
| 2 | 1 | < interval | One | unchanged | BDD-65 |
| 3 | 1000 | < interval | Very large burst | unchanged | BDD-65 |
| 4 | 1 | ≥ interval | Min above bound | exact counters | BDD-66 |
| 5 | 1000 | ≥ interval | Large + bound | exact counters, no double-count | BDD-66 |
| 6 | K | forced flush before interval | Alternate trigger | exact counters | BDD-67 |
| 7 | K | SIGKILL before interval | Resource loss | behind by ≤ K; transcript complete | BDD-70 |
| 8 | K on session A, 0 on session B | ≥ interval | Dirty-set selectivity | only A's file rewritten | BDD-59 |

#### Dataset: Sharding concurrency slope

| # | N concurrent sessions | Boundary type | Expected | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 1 | Min | baseline wall-clock `T1` | BDD-53 | Establishes the unit |
| 2 | 2 | Small | `< 2 × T1` | BDD-53 | Slope, not a constant |
| 3 | N (box-saturating) | Representative | `TN` recorded | BDD-53 | N chosen to saturate, not fixed by design |
| 4 | 2N | Max | `< 2 × TN` | BDD-53 | **The assertion.** Doubling must not double |
| 5 | N, all colliding on one shard by construction | Adversarial | serialises — documented, not a failure | BDD-53 | FNV collision behaviour is expected |
| 6 | N, pre-change store | Regression baseline | must be beaten by rows 3–4 | BDD-53 | Same box, same filesystem |

#### Dataset: WS frame identity stamping

| # | Producer | `session_id` | `producing_session_id` | Boundary type | Traces to |
|---|---|---|---|---|---|
| 1 | root turn | own id | absent | Root | BDD-12 |
| 2 | depth-1 child | chat id | child id | Direct child | BDD-16 |
| 3 | depth-3 grandchild | chat id | grandchild id | Deep nesting | BDD-16 |
| 4 | self-delegation | chat id | child id | Same agent, different session | BDD-16 |
| 5 | `rate_limit` (no `SessionID` field) | reconstructed | absent | Pre-existing strain | BDD-16 |
| 6 | `replay_done` (absent from `WsFrameType` enum) | routing id | absent | Pre-existing gap | BDD-16 |

### Regression Test Requirements

**This change MODIFIES existing behaviour.** The suite is the current contract's specification: ~430 references across ~71 test files touch the shared transcript id (128 `transcriptSessionID` refs across 43 test files alone).

**Behaviours that MUST be preserved exactly:**

| Existing behaviour | Existing test / anchor | New regression test | Why |
|---|---|---|---|
| Root turn WS routing is byte-identical | `pkg/gateway/replay_test.go:1549` | `TestRoutingSessionID_RootEqualsOwnSessionID` | Root is the overwhelming majority of traffic; a root regression is not acceptable collateral |
| Pre-arm latch "inherits verbatim" invariant | `pkg/agent/cancel_async_delegate_repro_test.go` | `TestPreArmLatch_KeysSetAndClearedMatch` | `pkg/agent/cancel_prearm.go:385-389` states the correctness argument explicitly |
| Chat-wide Stop reaches every live descendant | `pkg/agent/cancel_subagent_cascade_test.go:51-101`, `pkg/gateway/cancel_subagent_cascade_test.go:5` | `TestCancel_PhaseB…`, `TestCancel_PhaseC…` | This is what ADR-053's FR-6a amendment was protecting |
| Cancel isolation between unrelated sessions | `pkg/agent/cancel_session_isolation_test.go:12` | `TestCancel_KillsChildShellsNotSiblings` | A broader cascade must not become a wider blast radius |
| ADR-045 watchdog does not reap live critical work | `pkg/agent/orphan_watch_test.go:14,223-229` | `TestOrphanWatchdog_DefersWhileCriticalDelegateAlive` | Silent failure mode; the interlock's whole purpose |
| Orphan-delegate cancellation | `pkg/agent/cancel_orphan_delegate_test.go:57-79` | covered by #38–#42 | Same ladder |
| Steering delivery to a child at its next tool boundary | `pkg/agent/steering_test.go:1693,1765-1811,1865` | `TestDelegateControls_ResolveByChildID` | INV-3; a steer must still land |
| Approval-grant delegation inheritance | `pkg/agent/approval_grant_delegation_test.go:19,229` | `TestApprovalGrants_InheritKeyedToChildSession` | Availability: the 300 s invisible block |
| Subagent transcript nesting on the wire | `pkg/agent/subturn_transcript_nesting_test.go:9-10,93-94` | `TestFrameContract_BothIDsRoundTrip` | Span nesting key is a **different** field from the transcript one |
| Plan cancellation | `StopPlan` (`pkg/agent/plan_engine.go:2044-2135`) | no new test; **assert unchanged** | D9: explicitly out of scope |
| `list_jobs` attribution by `ParentAgentID` | existing `list_jobs` tests | `TestLifecycleFilter_ParentDurableKey_DirectChildrenOnly` (negative half) | A different axis; must not start filtering by session |
| `migrateLegacy` / `writeUnifiedMetaDirect` | existing migration tests | no new test; **assert unchanged** | Different legacy, out of scope |
| `make verify-contracts` clean | CI gate | `TestUnifiedMetaMarshal_ByteIdenticalAcrossSplit` | D11 must not drift the contract |

**Tests that MUST be deliberately inverted, never deleted (W22, U21, own commit):**

| File | Anchor | New assertion |
|---|---|---|
| `pkg/agent/subturn_test.go` | `TestSubTurnInheritsTranscriptSessionID` at `:2095`, equality at `:2143-2145` | The child's transcript session id is its **own**; the **routing** id is inherited |
| `pkg/agent/interrupt_by_session_key_test.go` | `:9-19`, `:232` | One scoped entry point; `ScopeSubtree` at a child spares parent and sibling |
| `pkg/agent/approval_grant_delegation_test.go` | `:19`, `:229` | Grant keyed to the child session |
| `pkg/agent/cancel_orphan_delegate_test.go` | `:57-79` | Cascade via the routing key |
| `pkg/agent/cancel_subagent_cascade_test.go` | `:51-101` | Same |
| `pkg/agent/cancel_session_isolation_test.go` | `:12` | Same |
| `pkg/agent/orphan_watch_test.go` | `:14`, `:223-229` | Interlock via the routing key |
| `pkg/agent/steering_test.go` | `:1693`, `:1765-1811`, `:1865` | Predicates re-based |
| `pkg/agent/subturn_transcript_nesting_test.go` | `:9-10`, `:93-94` | Nesting survives on the span key, not the transcript key |
| `pkg/agent/cancel_async_delegate_repro_test.go` | whole file | Pre-arm race still closed |
| `pkg/gateway/cancel_subagent_cascade_test.go` | `:5` | Gateway-side cascade |
| `pkg/gateway/replay_test.go` | `:1549` | Replay returns unfiltered entries |

**Regression dataset — behaviours a reviewer will otherwise mistake for breakage:**

| # | Input | Previous behaviour | Must now produce | Traces to |
|---|---|---|---|---|
| 1 | Pre-cutover session with delegate narration | narration hidden | narration **visible** | BDD-40 (accepted, R-16) |
| 2 | Parent reload after a delegation | parent's LLM context absorbed delegate narration | context **excludes** it | Regression: hydration (m-4) |
| 3 | `delegate action=cancel` on child B | cancels B's turn only, leaves B's children and shells | cancels B's **subtree** and kills B's shells | BDD-29, BDD-46 (R-13) |
| 4 | Grandchild `message_parent` | routed to the chat's inbox | routed to the **direct parent's** inbox | Regression: ADR-053 D16 (AC-16) |
| 5 | Per-child message ceiling in a chat | one shared pool | (children × ceiling) | Regression: ADR-053 D15 (AC-15) |
| 6 | Audit `session_id` for a child's action | the chat id | the **acting** session id | Regression: audit attribution |
| 7 | Child's loaded-tool manifest | inherited the parent's bucket | starts empty | Regression: token/latency cost per delegation |
| 8 | `follow_up` generation N+1 | no history | sees generation N's history | Regression: TDD #68 → AC-11 (intended, R-11) |

---

## Functional Requirements

### Strict transcript primitive and named types (W3, W20)

- **FR-001**: The system MUST provide `AppendTranscriptStrict`, which returns a non-nil error for a session id with no `meta.json` and MUST NOT create any directory for it.
- **FR-002**: All five transcript writer sites (`pkg/agent/turn.go:1130`, `:1208`, `:1270`, `:1325`; `pkg/gateway/websocket.go:4256`) plus `pkg/agent/external_dispatch.go:463`, `:550-555`, `:562-564` and `pkg/agent/approval_transcript.go:179`, `:183` MUST use the strict primitive and MUST surface its error as a counter increment plus a WARN naming the session id.
- **FR-003**: The `ts.abandoned` write suppression (`pkg/agent/turn.go:1296-1299`) MUST emit a counted, logged signal rather than returning silently.
- **FR-004**: The system MUST define `SessionID` and `RoutingSessionID` as distinct named types that do not implicitly interconvert.

### Child owns a real session (W1, W2)

- **FR-005**: Every delegated child MUST have a store-backed session created with the exact `childID`, via an exported wrapper over the existing exact-id primitive (`pkg/session/unified.go:441`).
- **FR-006**: The child's session meta MUST carry the parent's `Owner` verbatim, so `WithSessionOwner` installs inside the child turn (`pkg/agent/loop.go:6844-6848`).
- **FR-007**: The child's `processOptions` MUST NOT set `NoHistory` (today `true` at `pkg/agent/subturn.go:1032`), and `TranscriptSessionID` MUST equal `childID`.
- **FR-008**: `SessionMeta` MUST carry `ParentSessionID` naming the **direct** parent, and `UnifiedSessionType` MUST gain a subordinate value.
- **FR-009**: For a child, `delegateSessionID == sessionKey == transcriptSessionID` MUST hold, so `steer`/`respond`/`cancel`/`peek`/`inbox`/`follow_up` take the same single id they take today.
- **FR-010**: The child session MUST be minted into the **same** shared `*session.UnifiedStore` the delegate tool holds (`pkg/agent/loop.go:1727-1728`).

### Routing key (W4, W5, W21)

- **FR-011**: `turnState` MUST carry `routingSessionID`, inherited verbatim from the parent; for a root turn it MUST equal the turn's own session id.
- **FR-012**: Every session-scoped WS frame's `session_id` MUST be stamped from `routingSessionID`.
- **FR-013**: The system MUST add an optional `producing_session_id` to session-scoped frames, present **iff** it differs from `session_id`.
- **FR-014**: `routingSessionID` MUST NOT be read outside the closed consumer set (WS payload stamping, the seven role-B predicates, pre-arm keys), and a test MUST fail the build on any read outside it.
- **FR-015**: The seven role-B predicates (`pkg/agent/steering.go:429`, `:459`, `:519`, `:745`, `:787`; `pkg/agent/turn.go:524`, `:564`, `:607`) MUST re-base onto `routingSessionID`.
- **FR-016**: The pre-arm latch keys (`pkg/agent/cancel_prearm.go:338`, `:355`, `:602`; `pkg/agent/subturn.go:585`, `:1147`) MUST re-base onto `routingSessionID`, preserving the "inherits verbatim" invariant literally.
- **FR-017**: `SubTurnSpawnPayload.SessionID` and `SubTurnEndPayload.SessionID` MUST be pinned to `routingSessionID` with a regression test, and `DelegateTaskState.SessionID` (`pkg/tools/delegate.go:1303`) MUST be re-pointed deliberately.
- **FR-018**: All **19** `SESSION_SCOPED_FRAME_TYPES` MUST be audited against the routing rule, and the contract change MUST follow Constraint #8's 5-step pipeline in one atomic commit.

### Durable parent→child edge (W6, W7)

- **FR-019**: `LifecycleFilter` MUST gain a `ParentDurableKey` field and a corresponding `matches` clause.
- **FR-020**: A secondary parent index MUST be maintained inside `Persist`, under the existing 64-shard striped lock, so "children of X" is one file read and a transitive walk is O(descendants).
- **FR-021**: Delegation MUST be refused with an operator-visible error when no lifecycle store is wired (`pkg/agent/session_messaging_wire.go:141-143`), mirroring the existing fail-closed posture at `pkg/tools/delegate.go:1150-1157`.
- **FR-022**: The three doc comments at `pkg/session/lifecycle.go:225-228`, `:571-575` and `pkg/tools/list_jobs_sources.go:311-315` MUST be rewritten so none describes `ParentDurableKey` as shared parent↔child.
- **FR-023**: The system MUST NOT use `OwnerScopeID` or `ParentAgentID` as the parentage edge.

### Cancellation (W8, W9)

- **FR-024**: `RequestCancel` MUST compute the live subtree once in PHASE A and thread it through PHASE B (`pkg/agent/cancel.go:462`) and PHASE C (`:487`) rather than re-scanning.
- **FR-025**: The durable descendant walk MUST run once per Stop, on its own goroutine, off the escalation path.
- **FR-026**: Each descendant's lifecycle record MUST transition to `cancelled` (today `pkg/agent/cancel.go:428` transitions exactly one).
- **FR-027**: `ProcessSession.OwnerSessionID` MUST be stamped from the child's own id, and `KillBackgroundSessions` MUST cascade over the descendant set.
- **FR-028**: `delegate action=cancel` MUST kill that child's background shells (today no such call exists on that path).
- **FR-029**: A 3P child's process **group** MUST die with the child.
- **FR-030**: The `turn_canceled` audit entry's `descendants_canceled` (`pkg/agent/cancel.go:376`) MUST remain non-empty and name every descendant the Stop reached.

### Approvals and session teardown (W10)

- **FR-031**: `ApprovalGrantStore.Inherit`'s first argument MUST become the child's own session id.
- **FR-032**: `cancelAllPendingForSession` MUST run over the descendant set, not a single id.
- **FR-033**: A child session MUST receive a `CloseSession` on child-turn terminal, clearing its grant set, `loadedTools` bucket, `metaCache` entry and `recallSpans` entries.

### Transcript visibility (W11)

- **FR-034**: `IsDelegateChildEntry()` MUST be deleted and MUST have zero references outside tests.
- **FR-035**: All four filter sites MUST be deleted — `pkg/gateway/replay.go:298`, `pkg/gateway/rest.go:826` (including the `filterDelegateChildEntries` helper at `:823-832` and both callers `:851`/`:887`), `pkg/agent/verifier_adjudication.go:406`, `pkg/tools/inspect_session.go:172`.
- **FR-036**: `TranscriptEntry.ParentSpawnCallID` MUST be retained as provenance on the child's own entries and MUST have a named reader (the drill-down surface).
- **FR-037**: The three comment blocks that exist only to defend the filter (`pkg/session/daypartition.go:268-307`, `:311-332`; `pkg/gateway/replay.go:41-45` and `:271-297`) MUST be rewritten or removed in the same change.
- **FR-038**: No read boundary — backend or frontend — MAY reintroduce a transcript visibility filter.

### Ownership and interrupt (W12, W13)

- **FR-039**: `verifyCallerOwnsSession` MUST walk the `ParentDurableKey` chain upward from the target toward the caller, bounded by the configured max delegation depth, at all six gated call sites.
- **FR-040**: The sibling/cousin reach MUST be removed; root-over-subtree reach MUST be preserved.
- **FR-041**: `InterruptSession`, `InterruptSessionHard`, `InterruptBySessionKey` and `InterruptBySessionKeyHard` MUST collapse into one entry point taking a mandatory explicit `InterruptScope ∈ {ScopeSubtree, ScopeSelfOnly}`.
- **FR-042**: `Interrupt(child, ScopeSubtree)` MUST reach that child's own descendants and MUST NOT reach the parent or a sibling.

### Delegate observability (W14, W19)

- **FR-043**: `recentActivityLines` MUST read the delegate session id and MUST log its empty path.
- **FR-044**: `executeSync` MUST register a `DelegateTaskState` (today only `executeAsync` does).
- **FR-045**: `t.tasks` and `t.sessionIndex` MUST have a deletion path (today neither has one anywhere in the tree).
- **FR-046**: The drill-down surface (`GET /api/v1/sessions/{childID}` → `<ChatScreen />`) MUST be the stated inspection surface for hidden delegations and MUST work with verbose chat disabled.
- **FR-047**: No requirement MAY depend on `subagent_message` or `subagent_state` frames.

### Session store: striping (W15)

- **FR-048**: `UnifiedStore.mu` MUST be replaced by (a) a 64-shard FNV-keyed `sync.Mutex` pool keyed by session id, copying `pkg/session/lifecycle_lock.go:17-39`'s shape, and (b) a narrow `cacheMu sync.RWMutex` guarding only `metaCache` (`:182`) and `cacheLoadFailures` (`:192`).
- **FR-049**: `cacheMu` MUST NEVER be held across an `os.*` or `fileutil.*` call.
- **FR-050**: Lock order MUST be one-directional: `sessionLock(id)` → `cacheMu`. Two session shards MUST NOT be held at once, except by `ClearAll`/`RetentionSweep`, which MUST take every shard **in index order**.
- **FR-051**: `ListSessions` MUST reconcile per-session under that session's shard and snapshot under `cacheMu.RLock`, and MUST NOT take a store-global write lock.
- **FR-052**: The design MUST NOT impose a fixed concurrency cap; 64 shards matches the in-house precedent and does not bound throughput.

### Session store: file split (W23)

- **FR-053**: `meta.json` MUST split into four files — `meta.json` (identity + lifecycle + `Type` + `ParentSessionID`), `stats.json` (`SessionStats` + its own `UpdatedAt`), `goal.json` (the 9 `Goal*` fields), `loop.json` (the 9 `Loop*` fields).
- **FR-054**: `writeMetaLocked` MUST be replaced by four targeted writers, each taking its session's shard.
- **FR-055**: `readUnifiedMeta` MUST compose all four; a missing `stats.json`/`goal.json`/`loop.json` MUST compose as the zero value and MUST NOT be an error; a missing `meta.json` MUST be an error.
- **FR-056**: A present-but-corrupt group file MUST surface an error for that group rather than composing a zero value.
- **FR-057**: `UnifiedMeta`'s in-memory shape and marshalled JSON MUST be unchanged; no `contracts/` change and no regeneration are required by this work item.
- **FR-058**: `metaCache` MUST continue to hold one composed `*UnifiedMeta` clone per session, so `GetMeta` and `ListSessions` cost nothing extra.
- **FR-059**: The doc comments at `pkg/session/unified.go:780-785` and `:166-181` MUST be rewritten, as neither single-funnel claim remains true.
- **FR-060**: The system MUST NOT provide a reader for a pre-split fused `meta.json`, and MUST NOT modify `migrateLegacy`/`writeUnifiedMetaDirect` (`:1515`).

### Session store: counter throttle (W24)

- **FR-061**: `AppendTranscript`'s `Stats.*` and `UpdatedAt` bumps MUST become in-memory mutations of the cached meta under `cacheMu`, with no file write.
- **FR-062**: The transcript append itself MUST stay immediate and unthrottled.
- **FR-063**: A per-store periodic flusher MUST write only `stats.json`, only for dirty sessions, each write taking that session's shard.
- **FR-064**: Forced synchronous flushes MUST occur on a `SetMeta` carrying `Status`, on `DeleteSession`, on `UnifiedStore.Close` (which has no flush hook today), and on the child `CloseSession` teardown.
- **FR-065**: Event-driven `SetMeta` paths (goal, loop, status, title, owner, workspace) MUST NOT be throttled.
- **FR-066**: `UpdatedAt` MUST compose as the later of `meta.json`'s and `stats.json`'s on load.
- **FR-067**: The flush interval SHOULD be a config key with a default in the seconds range, tunable from measurement, not an operator decision about the design.

### Scale and hygiene (W16, W17, W18)

- **FR-068**: `GET /api/v1/sessions` MUST paginate through all four layers, and the sidebar MUST filter subordinate sessions so a wide fan-out cannot evict the parent chat.
- **FR-069**: Root-level delegation MUST be admission-gated, refusing rather than queueing when the cap is reached, with an operator-visible refusal.
- **FR-070**: Nested delegation's existing `concurrencySem` gating MUST be unchanged.
- **FR-071**: A child's uploads directory MUST be reachable by the parent session's cascade-delete, for every descendant.

### Process (W22)

- **FR-072**: Every test encoding the current contract MUST be deliberately inverted to assert the new invariant; none MAY be quietly deleted.
- **FR-073**: The test inversions MUST land as their own commit, containing no behaviour-file change.
- **FR-074**: Every test written or inverted for this spec MUST construct parent and child ids as distinct, non-equal values and MUST assert which one was used.

### Consequential semantics (US-18)

- **FR-075**: `follow_up` warm resume MUST load generation N's history into generation N+1 — this is intended behaviour, not a leak, and MUST be pinned by test.
- **FR-076**: The ADR-053 D15 per-child message ceiling MUST be enforced per **direct parent**, making a chat's aggregate (children × ceiling); the change MUST be asserted, not assumed.
- **FR-077**: The ADR-053 D16 inbox producer (`pkg/tools/message_parent.go:640`) and consumers (`pkg/tools/delegate.go:2024`, `:2200`) MUST both key on the immediate parent's `ParentDurableKey` and MUST change together.
- **FR-078**: The boot sweep MUST reconcile an in-flight child's lifecycle record across a process restart, and no transcript write MAY land in a directory with no `meta.json`.

---

## Success Criteria

- **SC-001**: `AppendTranscriptStrict` against a UUID with no `meta.json` returns a non-nil error in 100 % of trials and creates zero directories, verified by `os.Stat`.
- **SC-002**: After one delegation, `<store>/<childID>/meta.json` exists on disk and `GET /api/v1/sessions/{childID}` returns HTTP 200 with a non-empty `messages` array.
- **SC-003**: `rg -n "IsDelegateChildEntry" --glob '!*_test.go'` returns zero matches.
- **SC-004**: After one delegation, the parent session's `transcript.jsonl` contains zero entries produced by the child, measured by reading the file.
- **SC-005**: In the live-connection E2E, the SPA store's chat bucket contains both the subagent span and 100 % of its tool-call steps, and `chatAttachStepSpanIndexMiss` fires zero times.
- **SC-006**: All 19 `SESSION_SCOPED_FRAME_TYPES` round-trip both `session_id` and `producing_session_id` per the stamping matrix, with zero types unaudited.
- **SC-007**: `List(LifecycleFilter{ParentDurableKey: X})` returns exactly the direct children of X — zero grandchildren, zero siblings — at depths 1, 2 and 3.
- **SC-008**: A chat-level Stop against a live `Critical:true` child produces a PHASE B hard abort and a PHASE C detach against that child, and `descendants_canceled` has length ≥ 1 naming it.
- **SC-009**: A chat-level Stop leaves zero live PIDs among the subtree's background shells and ≥ 1 live PID for an unrelated sibling chat's shell.
- **SC-010**: A `delegate action=cancel` on child B leaves zero live PIDs for B's shells while parent A and sibling C remain in a running state.
- **SC-011**: With a standing parent grant, a delegated child's granted tool call completes with zero approval prompts and elapsed time under the approval timeout by at least two orders of magnitude.
- **SC-012**: After a child terminates, lookups for its grant set, `loadedTools` bucket and `recallSpans` entries all return absent.
- **SC-013**: A sibling's attempt at each of the six gated actions against another sibling returns an ownership error in 6 of 6 cases; the root chat's attempt against a grandchild succeeds in 6 of 6.
- **SC-014**: The interrupt API exposes exactly one entry point (plus its `Hard` variant) and the scope argument is non-optional, proven by a compile-fail fixture.
- **SC-015**: `delegate action=status` returns a non-empty activity snapshot for both a sync and an async delegation.
- **SC-016**: Wall-clock for 2N concurrent single-session writers is less than 2× the wall-clock for N, on the same box and filesystem, with the pre-change store's measurement recorded as the baseline that must be beaten.
- **SC-017**: A `-race` run over concurrent create/append/`SetMeta`/`ListSessions`/`DeleteSession` on overlapping and disjoint ids reports zero data races and completes without deadlock.
- **SC-018**: Zero `cacheMu` critical sections contain an `os.*` or `fileutil.*` call, verified by a static gate.
- **SC-019**: After a create plus one `/goal set`, one `/loop` start and one transcript append, the session directory contains exactly the four expected files and zero fields appear in more than one of them.
- **SC-020**: A `/loop` tick leaves `goal.json` byte-identical, a `/goal` round leaves `loop.json` byte-identical, and a transcript append leaves both byte-identical — 3 of 3.
- **SC-021**: `readUnifiedMeta` returns success for a directory with only `meta.json` and an error for a directory with no `meta.json`, in both directions, with `GET /api/v1/sessions/{id}` returning 404 in the latter case.
- **SC-022**: `UnifiedMeta`'s marshalled JSON and the REST session payload are byte-identical pre- and post-split for the same logical state, and `make verify-contracts` exits 0.
- **SC-023**: During a burst of K appends inside one flush interval, `stats.json`'s mtime and byte content are unchanged and `transcript.jsonl` gains exactly K lines.
- **SC-024**: After the flush interval elapses, `stats.json`'s counters equal the exact sum of the appended entries' deltas — zero lost and zero double-counted.
- **SC-025**: Each of the four forced flush points independently leaves `stats.json` current, verified by re-opening the store and comparing counters exactly.
- **SC-026**: `GoalRoundsUsed`, `LoopRunCount`, `Status` and `Title` are each readable from disk immediately after their call returns, with zero flush interval elapsed — 4 of 4.
- **SC-027**: After a SIGKILL mid-interval and a re-open, the counter shortfall is at most that interval's appends and `transcript.jsonl` is complete.
- **SC-028**: With verbose chat disabled, the drill-down surface renders a hidden delegation's transcript using only `GET /api/v1/sessions/{childID}`.
- **SC-029**: With 24 child sessions created after a parent chat, the sidebar still lists that parent chat.
- **SC-030**: With a root-delegation cap of N and N in flight, the N+1th root-level delegation is refused with an operator-visible error and zero of them are queued.
- **SC-031**: Deleting a parent session removes `<home>/uploads/<id>/` for 100 % of its descendants.
- **SC-032**: All twelve named gate test files exist and assert the new invariant; zero are deleted.
- **SC-033**: The W22 commit's file list contains zero non-`_test.go` files.
- **SC-034**: `gofmt -l . | wc -l` is 0, `golangci-lint run --build-tags=goolm,stdjson` exits 0, `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 ./...` exits 0 in CI, `govulncheck ./...` reports 0 vulnerabilities, `npm run typecheck` and `npx vitest run` exit 0, and `make verify-contracts` exits 0.

---

## Acceptance Criteria (verbatim from ADR-057 v4 §10)

> These are carried forward **unchanged and non-negotiable**. Where this spec's Functional Requirements, BDD scenarios or Success Criteria appear to differ in wording, **the ADR text below governs**.

**The governing fact.** Almost every failure in this migration is *success-shaped*: a predicate returns "nothing to do" and every caller proceeds happily. This project's precedent is `plan_engine.go:3937-3944` — a derived `plan:<id>` id that cancelled nothing in production for months while every test passed, because the fake canceller recorded the string it was handed and returned success.

**Three consequences.**

1. **Every criterion below is verified against real store-backed state and real registered turns. A spy or mock that records its argument and returns success is disallowed, without exception.**
2. **The v4 storage criteria (AC-20/21/22) are held to that same bar, and they need it most.** Their failure modes are the quietest in this document: a counter that is 300 tokens light is indistinguishable from a correct one, a re-serialised store is only slower, and a re-added filter still returns a valid response. So AC-20 asserts a **slope** (doubling concurrency must not double wall-clock) rather than a call count; AC-21 asserts on the **session directory's files and their bytes**, not on the composed struct that would look identical either way; and AC-22 asserts on **`stats.json`'s mtime and contents** across a real interval, not on whether a flush function was invoked. The precedent for why this matters is the same one below: a fake that records the string it was handed and returns success proved nothing for months. `pkg/entity/store_crossprocess_test.go` — which re-execs the test binary as real OS processes — is the in-house shape to copy.
3. **AC-1 comes first and gates the rest.** Until `AppendTranscript` fails loudly, a green suite is not evidence: today it `MkdirAll`s the directory, writes the line, fails `readMetaLocked`, logs `slog.Warn("unified_store: could not update meta stats")` and **returns `nil`** (`pkg/session/unified.go:814-823`); `ReadTranscript` on a missing path returns `[]TranscriptEntry{}, nil` (`:1194-1196`). It is a silent **create**, not a silent drop — so an assertion of the form "the append succeeded" can never fail.

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
| **AC-18** | R-16, D6 | **Rewritten in v4 for greenfield — the pre-cutover invariant v3 asserted here is deliberately abandoned.** (a) A repo-wide assertion that `IsDelegateChildEntry` has **zero** references outside tests, and that none of the four read boundaries filters on `ParentSpawnCallID`. (b) After one delegation, the **parent's** `transcript.jsonl` contains no child entry at all — asserted on the file, structurally, not on a rendered response, so the property cannot be satisfied by a filter someone re-adds. (c) On the child's own session, `inspect_session` and `GET /api/v1/sessions/{childID}` return the full transcript. (d) `TranscriptEntry.ParentSpawnCallID` is still stamped on the child's own entries and is read by W19's drill-down. (e) The verifier's window (`verifier_adjudication.go:403`) receives the adjudicated session's own entries and nothing else |
| **AC-19** | migration | A session **in flight** across a deploy: the parent's turn is mid-delegation when the process restarts. Assert the boot sweep reconciles the child's lifecycle record and no transcript write lands in an orphan directory |
| **AC-20** | R-8, R-19 | **D10 sharding — measured against a real on-disk store, never a mock or an in-memory fake.** (a) **Concurrent writes to DIFFERENT sessions do not serialise:** N goroutines each create a session and append transcript lines to their own session concurrently; assert wall-clock completion is close to the *single*-session time, not N× it, on the same box and filesystem — with the same test run against the pre-change store as the baseline it must beat. N is chosen to saturate the box, not fixed by the design (operator: "as many as the box allows"); the assertion is on the **slope** — doubling N must not double the time — so the criterion does not encode a machine-specific constant. (b) `ListSessions` concurrent with an in-flight `NewSession` on an unrelated session does not block on it. (c) Streaming appends to session A are not delayed by a session create for session B (the specific R-8 regression). (d) A lock-order assertion: a race-detector run (`-race`) over concurrent create/append/`SetMeta`/`ListSessions`/`DeleteSession` on overlapping and disjoint ids is clean, and `ClearAll`/`RetentionSweep` interleaved with per-session writes neither deadlocks nor drops a session. (e) Static/review gate: no `cacheMu` critical section contains an `os.*` or `fileutil.*` call |
| **AC-21** | R-17, D11 | **The file split — asserted on the directory, not on the in-memory struct.** (a) After a create plus one `/goal set`, one `/loop` start and one transcript append, the session directory contains `meta.json`, `stats.json`, `goal.json`, `loop.json`, and each file contains **only** its own group's fields. (b) **Writer isolation, byte-level:** a `/loop` tick leaves `goal.json`'s bytes unchanged; a `/goal` round leaves `loop.json`'s unchanged; a transcript append leaves both unchanged. (c) **Composition:** a session directory with `meta.json` only loads successfully with zero-valued stats/goal/loop; a directory with **no** `meta.json` returns an error from `readUnifiedMeta` and 404s through `GET /api/v1/sessions/{id}` — the asymmetry is asserted in both directions, because inverting it re-opens R-7. (d) **Partial-write:** with `goal.json` present but truncated/corrupt, the load surfaces an error for that group rather than silently composing a zero goal. (e) `UnifiedMeta`'s marshalled JSON and every REST/WS payload are byte-identical to pre-split for the same logical state (no contract drift; `make verify-contracts` unaffected). (f) Doc-truth gate, as AC-13: `writeMetaLocked`'s (`:780-785`) and `metaCache`'s (`:166-181`) comments no longer assert a single whole-document write funnel |
| **AC-22** | R-18, D12 | **The throttle — asserted against real store-backed state, with a real clock or an injected fake, never a spy that records its argument.** (a) During a burst of appends within one flush interval, `stats.json`'s **mtime and bytes do not change**, while `transcript.jsonl` grows by exactly one line per append — proving the transcript stayed immediate and only the counters were deferred. (b) After the interval elapses, `stats.json` on disk matches the counters implied by the appended entries **exactly** (no lost or double-counted delta). (c) **Forced flush points each verified independently:** a `SetMeta` with a `Status` patch, `DeleteSession`, and `UnifiedStore.Close` each leave `stats.json` current; re-opening the store reads back the exact counters. (d) **Event-driven writes are provably not throttled:** a `/goal` round's `GoalRoundsUsed`, a `/loop` tick's `LoopRunCount`, a `Status` transition and a `Title` change are each on disk **immediately** after the call returns, with no flush interval elapsed. (e) **Ordering:** `ListSessions` returns a session that just streamed ahead of one that streamed earlier, with no flush in between (the in-memory `UpdatedAt` bump, `:1289-1290`). (f) **The accepted loss is bounded and asserted:** kill the process mid-interval and re-open; the counters are behind by at most the interval's appends and the transcript is complete — asserted, so the loss window is a measured property rather than a hope |

**m-5's warning applies to the whole suite:** `pkg/agent/message_parent_real_context_test.go:16-17` already notes its fixture *"happens to make `ToolTranscriptSessionID`"* equal the seeded id — i.e. an existing test would **not** catch a divergence introduced here. Every criterion above must construct the parent and child ids as *distinct values* and assert on which one was used.

> **Two AC citations differ from the verified tree** (see §"Citation corrections"): AC-1's `websocket.go:4254` is the `ParentSpawnCallID` stamp — the `AppendTranscript` call to convert is `:4256`; and consequence 3's `ReadTranscript` reference `:1194-1196` is verified at `:1192-1194`. Neither changes the criterion.

---

## Traceability Matrix

| Requirement | User Story | Work item(s) | BDD Scenario(s) | Test(s) | ADR AC |
|---|---|---|---|---|---|
| FR-001 | US-1 | W3 | BDD-01 | #1 | AC-1 |
| FR-002 | US-1 | W3 | BDD-02, BDD-03 | #2, #5, #6 | AC-1 |
| FR-003 | US-1 | W3 | BDD-04 | #7 | AC-1 |
| FR-004 | US-1 | W20 | BDD-05 | #3 | AC-2 |
| FR-005 | US-2 | W1 | BDD-06 | #4, #32 | AC-1 |
| FR-006 | US-2 | W1 | BDD-07, BDD-08 | #4, #34 | AC-2 |
| FR-007 | US-2 | W1 | BDD-09 | #33 | AC-11 |
| FR-008 | US-2 | W2 | BDD-10 | #35 | AC-14 |
| FR-009 | US-2 | W1 | BDD-11 | #36 | AC-8 |
| FR-010 | US-2 | W1 | BDD-06 | #32 | AC-1 |
| FR-011 | US-3 | W4 | BDD-12, BDD-13 | #28, #37 | AC-2 |
| FR-012 | US-3 | W4, W5 | BDD-14, BDD-16 | #75, #76 | AC-3 |
| FR-013 | US-3 | W5 | BDD-16 | #75 | AC-3 |
| FR-014 | US-3 | W4 | BDD-17 | #29 | AC-2 |
| FR-015 | US-3, US-5 | W4 | BDD-23, BDD-24, BDD-26, BDD-27 | #38, #39, #41, #42 | AC-4, AC-5 |
| FR-016 | US-3 | W4 | BDD-13 | #37 | AC-4 |
| FR-017 | US-3 | W21 | BDD-14, BDD-15 | #76, #77 | AC-3 |
| FR-018 | US-3 | W5 | BDD-16 | #75 | AC-3 |
| FR-019 | US-4 | W6 | BDD-18 | #10 | AC-17 |
| FR-020 | US-4 | W6 | BDD-19 | #11 | AC-17 |
| FR-021 | US-4 | W7 | BDD-20 | #48 | AC-17 |
| FR-022 | US-4 | W6 | BDD-22 | #12 | AC-13 |
| FR-023 | US-4 | W6 | BDD-18, BDD-21 | #10, #49 | AC-17 |
| FR-024 | US-5 | W8 | BDD-23, BDD-24 | #38, #39 | AC-4 |
| FR-025 | US-5 | W8 | BDD-30 | #45 | AC-4 |
| FR-026 | US-5 | W8 | BDD-30 | #45 | AC-4 |
| FR-027 | US-5 | W9 | BDD-28 | #43 | AC-6 |
| FR-028 | US-5 | W9 | BDD-29 | #44 | AC-6 |
| FR-029 | US-5, US-18 | W9 | BDD-86 | #68a | AC-17 |
| FR-030 | US-5 | W8 | BDD-25 | #40 | AC-4 |
| FR-031 | US-6 | W10 | BDD-31, BDD-32 | #25 | AC-7 |
| FR-032 | US-6 | W10 | BDD-33 | #46 | AC-7 |
| FR-033 | US-6 | W10 | BDD-34 | #47 | AC-7 |
| FR-034 | US-7 | W11 | BDD-35, BDD-40 | #58, #61 | AC-18, R-16 |
| FR-035 | US-7 | W11 | BDD-35, BDD-37, BDD-39 | #57, #58, #60 | AC-18 |
| FR-036 | US-7 | W11 | BDD-38 | #59 | AC-18 |
| FR-037 | US-7 | W11 | BDD-35 | #58 | AC-18 |
| FR-038 | US-7 | W11 | BDD-36 | #56 | AC-18 |
| FR-039 | US-8 | W12 | BDD-42, BDD-44 | #50, #52 | AC-8 |
| FR-040 | US-8 | W12 | BDD-41, BDD-43 | #50, #51 | AC-8 |
| FR-041 | US-9 | W13 | BDD-45, BDD-48 | #27, #53 | AC-8 |
| FR-042 | US-9 | W13 | BDD-46, BDD-47 | #53, #54 | AC-8 |
| FR-043 | US-10 | W14 | BDD-51 | #30 | AC-9 |
| FR-044 | US-10 | W14 | BDD-49, BDD-50 | #55 | AC-9 |
| FR-045 | US-10 | W14 | BDD-52 | #31 | AC-9 |
| FR-046 | US-14 | W19 | BDD-71, BDD-74 | #78 | AC-14 |
| FR-047 | US-14 | W19 | BDD-74 | #78 | AC-14 |
| FR-048 | US-11 | W15 | BDD-53, BDD-55, BDD-57 | #8, #69, #70, #72 | AC-20 |
| FR-049 | US-11 | W15 | BDD-57 | #9 | AC-20 |
| FR-050 | US-11 | W15 | BDD-56, BDD-57 | #9, #73 | AC-20 |
| FR-051 | US-11 | W15 | BDD-54 | #71 | AC-20 |
| FR-052 | US-11 | W15 | BDD-53 | #70 | AC-20 |
| FR-053 | US-12 | W23 | BDD-58 | #13 | AC-21 |
| FR-054 | US-12 | W23 | BDD-59 | #17 | AC-21 |
| FR-055 | US-12 | W23 | BDD-60, BDD-61 | #14, #15 | AC-21 |
| FR-056 | US-12 | W23 | BDD-62 | #16 | AC-21 |
| FR-057 | US-12 | W23 | BDD-63 | #18 | AC-21 |
| FR-058 | US-12 | W23 | BDD-58 | #13 | AC-21 |
| FR-059 | US-12 | W23 | BDD-64 | #19 | AC-21 |
| FR-060 | US-12 | W23 | BDD-61 | #15 | AC-21 |
| FR-061 | US-13 | W24 | BDD-65 | #20 | AC-22 |
| FR-062 | US-13 | W24 | BDD-65 | #20 | AC-22 |
| FR-063 | US-13 | W24 | BDD-66 | #21 | AC-22 |
| FR-064 | US-13 | W24 | BDD-67 | #22 | AC-22 |
| FR-065 | US-13 | W24 | BDD-68 | #23 | AC-22 |
| FR-066 | US-13 | W24 | BDD-69 | #24 | AC-22 |
| FR-067 | US-13 | W24 | BDD-70 | #74 | AC-22 |
| FR-068 | US-14 | W16 | BDD-72, BDD-73 | #79, #80 | AC-10 |
| FR-069 | US-15 | W17 | BDD-75, BDD-77 | #63 | AC-10 |
| FR-070 | US-15 | W17 | BDD-76 | #64 | AC-10 |
| FR-071 | US-16 | W18 | BDD-78, BDD-79 | #26, #62 | AC-12 |
| FR-072 | US-17 | W22 | BDD-80 | #81 | AC-8 |
| FR-073 | US-17 | W22 | BDD-81 | #82 | AC-8 |
| FR-074 | US-17 | W22 | BDD-82 | #83 | all (m-5) |
| FR-075 | US-18 | W1 | BDD-83 | #68 | AC-11 |
| FR-076 | US-18 | W12 | BDD-84 | #67 | AC-15 |
| FR-077 | US-18 | W12, W14 | BDD-85 | #66 | AC-16 |
| FR-078 | US-18 | W6, W17 | BDD-87 | #65 | AC-19 |

**Completeness check**: 78 FRs, every row carrying at least one BDD scenario and at least one test. 87 BDD scenarios, every one of which appears in at least one row (BDD-01 … BDD-87, contiguous). Every ADR acceptance criterion AC-1 … AC-22 is referenced by at least one row.

### Work-item coverage map (W1 … W24 — nothing deferred)

| W | Summary | User Story | Work Unit | FRs |
|---|---|---|---|---|
| W1 | Exact-id session create; copy parent `Owner`; delete `NoHistory` | US-2, US-18 | U2 (store), U7 (agent) | FR-005…FR-007, FR-009, FR-010, FR-075 |
| W2 | `SessionMeta.ParentSessionID` + subordinate `UnifiedSessionType` + OpenAPI + SPA | US-2, US-14 | U5 (store), U10 (contract), U12 (SPA) | FR-008 |
| W3 | `AppendTranscriptStrict` + convert all writers | US-1 | U2, U3, U11 | FR-001…FR-003 |
| W4 | `turnState.routingSessionID`; re-base 7 predicates + pre-arm keys | US-3 | U3, U7, U8, U9, U15 | FR-011, FR-014…FR-016 |
| W5 | WS contract: routing key + `producing_session_id`; audit 19 frame types | US-3 | U10, U11, U12 | FR-012, FR-013, FR-018 |
| W6 | `LifecycleFilter.ParentDurableKey` + parent index + 3 doc rewrites | US-4, US-18 | U13, U14 | FR-019, FR-020, FR-022, FR-023, FR-078 |
| W7 | Refuse delegation with no lifecycle store | US-4 | U14, U19 | FR-021 |
| W8 | Subtree computed once in PHASE A; durable walk; per-descendant transitions | US-5 | U15 | FR-024…FR-026, FR-030 |
| W9 | Shell ownership + cascade kill + `delegate cancel` kills shells + 3P group | US-5, US-18 | U14, U16 | FR-027…FR-029 |
| W10 | Grants re-keyed; pending-approval teardown; child `CloseSession` | US-6 | U7, U9, U11, U17 | FR-031…FR-033 |
| W11 | Delete `IsDelegateChildEntry` + 4 filter sites + 3 comment blocks | US-7 | U5 (predicate), U18 (sites) | FR-034…FR-038 |
| W12 | Ancestor-chain ownership walk at 6 call sites | US-8, US-18 | U14 | FR-039, FR-040, FR-076, FR-077 |
| W13 | One interrupt entry point with explicit `InterruptScope` | US-9 | U8 | FR-041, FR-042 |
| W14 | `recentActivityLines` fix; `executeSync` registers state; map deletion path | US-10 | U14 | FR-043…FR-045 |
| W15 | Stripe `UnifiedStore.mu`; narrow `cacheMu`; one-directional lock order | US-11 | U4 | FR-048…FR-052 |
| W16 | Pagination through all four layers; sidebar filter | US-14 | U12, U18 | FR-068 |
| W17 | Root-level delegation admission gate | US-15, US-18 | U19 | FR-069, FR-070, FR-078 |
| W18 | Child uploads directory reachable by cascade-delete | US-16 | U20, U18 | FR-071 |
| W19 | Drill-down surface as the stated inspection surface | US-14 | U12, U18 | FR-046, FR-047 |
| W20 | Named ID types (`SessionID`, `RoutingSessionID`) | US-1 | U1 | FR-004 |
| W21 | Pin `SubTurn*Payload.SessionID`; re-point `DelegateTaskState.SessionID` | US-3 | U7, U14 | FR-017 |
| W22 | Deliberately invert the 12 gate tests, in their own commit | US-17 | U21 | FR-072…FR-074 |
| W23 | Split `meta.json` into four files + 2 doc rewrites | US-12 | U5 | FR-053…FR-060 |
| W24 | Throttle the counter path; forced flush points; event writes immediate | US-13 | U6 | FR-061…FR-067 |

**Hardest to place, stated honestly:**

- **W2** is split three ways (store field, OpenAPI enum, SPA enum) across three units. Its ADR justification also *narrowed* in v4 — with the filter deleted, the "filter discriminator" rationale is gone and only R-9 (listing) and W19 (drill-down) remain. It survives on those two.
- **W17** sits between US-15 and US-18: it is a concurrency gate, but its acceptance evidence (`docs/internal/uat/max-parallel-concurrency-gap-2026-07-31.md` §G1) is a UAT observation rather than a code-derived requirement, and it is required *by* this ADR rather than *of* it.
- **W22** is a process requirement, not a behaviour. It gets a P0 user story because the commit shape is the only thing that keeps bisection honest, and because ~430 references make "quietly delete the failing test" the path of least resistance.
- **W11's predicate deletion vs its call sites** land in two different units (U5 owns `daypartition.go`, U18 owns the four call sites) purely for file-ownership safety. They must land in the same integration window or the tree does not compile.

---

## Ambiguity Self-Audit

> Take these to the operator. None of them blocks starting Wave A.

| # | What's ambiguous | Likely agent assumption (what will happen if nobody answers) | Question to resolve |
|---|---|---|---|
| 1 | **R-9 listing policy** — ADR §9's one remaining open question. Are subordinate sessions hidden by default with an opt-in flag (the `verifier` precedent, `pkg/gateway/rest.go:783-785` + `?include_verifier=true`), or shown nested under their parent? | The agent will copy the `verifier` precedent: hidden by default, `?include_subordinate=true`. It is the closest in-tree pattern and the lowest-risk default | Hidden-with-flag, or nested-under-parent? W2 supplies the data either way; only the SPA treatment differs |
| 2 | **D12 flush interval default.** ADR §9 says explicitly this is a tuning value, not a design question — "any value in the seconds range satisfies both" constraints | The agent will pick 5 s and expose it as a config key with that default | Confirm 5 s, or name a preferred default? |
| 3 | **AC-10's "stated budget" for inter-token latency** is not stated anywhere. AC-20 solves the same problem by asserting a slope; AC-10 still says "within a stated budget" | The agent will convert AC-10(a) into a slope assertion too, using the pre-change store as the baseline, rather than inventing a millisecond constant | Is a slope assertion acceptable for AC-10, or does the operator want a concrete p95 budget? |
| 4 | **Root-level delegation cap value (W17).** The UAT observed "24 parallel against a cap of 16"; the ADR does not name the new cap | The agent will reuse the existing `maxConcurrent` config value rather than introduce a second knob | Should root-level fan-out share `maxConcurrent`, or get its own key? |
| 5 | **`InterruptScope` default at existing call sites.** D8 makes the scope mandatory, but does not say which scope each of today's callers gets | The agent will map `InterruptSession*` → `ScopeSubtree` and `InterruptBySessionKey*` → `ScopeSubtree` **rooted at the child** (per D8's reconciliation of #577), which is a deliberate behaviour change (R-13) | Confirm `delegate action=cancel` should become subtree-scoped — this is R-13 and it is a real behaviour change |
| 6 | **What "operator-visible" means for a refused delegation** (W7, W17). The ADR says "operator-visible error, never a silent skip" without naming the surface | The agent will return a tool error to the calling agent **and** emit `slog.Error`, mirroring `pkg/tools/delegate.go:1150-1159`'s existing shape | Is a tool-result error + `slog.Error` sufficient, or is a user-facing notification required? |
| 7 | **Corrupt-group-file recovery (FR-056).** AC-21(d) requires an error for a corrupt `goal.json`, but does not say whether the session as a whole becomes unloadable | The agent will surface a per-group error while still loading `meta.json`, so the session remains listable and deletable rather than becoming a permanently stuck row | Should a corrupt group file make the whole session unloadable, or only that group? |
| 8 | **`cacheLoadFailures` semantics after striping.** The existing counter documents an accepted limitation: a session that fails to load at construction is excluded for the process lifetime (`pkg/session/unified.go:184-192`) | The agent will preserve that behaviour exactly, since changing it is out of scope | Confirm this accepted limitation stays as-is under W15 |
| 9 | **Whether `ParentSpawnCallID` should also be persisted on child tool-call and error entries.** Today only three writers stamp it; `appendToolCallTranscript` and `appendErrorTranscript` do not | The agent will leave the three writers as-is — the field's new job is provenance for W19's drill-down, and widening it is unrequested scope | Should provenance be complete across all entry types, or is partial stamping fine? |
| 10 | **Audit-query impact.** ADR §3.1 marks "no aggregation consumer groups audit entries by chat session" as **[INFERRED]** — it was not verified, only unfound | The agent will proceed on the inference and note it, since the ADR does | Does any operator dashboard or export group audit rows by chat `session_id`? If yes, it needs a `ParentDurableKey` join |
| 11 | **`replay_done` and `rate_limit` frame gaps.** Both are pre-existing strains this change *exposes*: `rate_limit` has no `SessionID` field at all, and `replay_done` is absent from the `WsFrameType` enum | The agent will audit them per W5, document the gap, and **not** fix them — they are named in the ADR as pre-existing and not caused here | Fix in scope, or track separately? |
| 12 | **How `metaCache` interacts with the D12 dirty set.** D12 mutates the cached meta in memory; D11 caches a *composed* clone. The ADR does not say whether the dirty set lives on the cache entry or beside it | The agent will keep a separate dirty-session set guarded by `cacheMu`, so the cache entry stays a plain clone | Confirm, or specify a preferred shape |

---

## Evaluation Scenarios (Holdout)

> **HOLDOUT — post-implementation evaluation only.** These MUST NOT be visible to the implementing agent during development. They are deliberately absent from the TDD plan, the datasets and the traceability matrix, and no FR or BDD scenario references them.

### H-1 — A four-deep chain cancels completely from the root
- **Setup**: chat `A` → child `B` → grandchild `C` → great-grandchild `D`, all live, `D` running a real background `bash`.
- **Action**: a single chat-level Stop on `A`.
- **Expected outcome**: all four turns reach a cancelled state, all four lifecycle records read `cancelled`, `D`'s real PID is gone, and the `turn_canceled` audit entry names three descendants.
- **Category**: Happy Path

### H-2 — Two unrelated chats delegating concurrently stay isolated
- **Setup**: chats `P` and `Q`, each with two live children, run by the **same** agent id.
- **Action**: Stop `P`.
- **Expected outcome**: `P`'s two children are cancelled; `Q`'s two children are untouched and complete normally. Nothing in `Q` appears in `P`'s audit descendant list.
- **Category**: Happy Path

### H-3 — A delegation that streams heavily leaves an exact, complete record
- **Setup**: one child streams ≥ 500 assistant tokens across ≥ 50 transcript entries with a mix of models.
- **Action**: let the child complete, then close the store gracefully and re-open it.
- **Expected outcome**: `transcript.jsonl` has exactly the entries appended; `stats.json`'s `TokensTotal`, `Cost`, `ToolCalls`, `MessageCount` and per-model `ByModel` breakdown match the entries exactly; `goal.json` and `loop.json` are absent or zero-valued.
- **Category**: Happy Path

### H-4 — A delegation attempted with a read-only session directory
- **Setup**: make the sessions base directory read-only immediately before a delegation.
- **Action**: dispatch one delegation.
- **Expected outcome**: the session create fails with a non-nil, operator-visible error; the delegation is refused; **no** turn runs against a session that does not exist; and no transcript line is written anywhere.
- **Category**: Error

### H-5 — Ownership walk against a lifecycle record whose parent record was deleted
- **Setup**: a depth-3 tree; delete the depth-2 record from the lifecycle store out of band.
- **Action**: the root chat invokes `delegate action=cancel` on the depth-3 child.
- **Expected outcome**: the walk terminates on the broken link and **rejects** the action with an ownership error; it does not fall through to permit, and it does not scan the whole store looking for a path.
- **Category**: Error

### H-6 — Two processes writing the same session's stats concurrently
- **Setup**: two OS processes (re-exec'd test binaries) open stores rooted at the same directory and both stream into the same session id.
- **Action**: let both flush.
- **Expected outcome**: on POSIX, `stats.json` is a valid, parseable document with no interleaved bytes and no lost file; the documented Windows limitation (no cross-process locking, `pkg/fileutil/flock_windows.go`) is stated rather than silently assumed away.
- **Category**: Edge Case

### H-7 — A child whose id collides with an existing session directory
- **Setup**: pre-create a session directory whose name equals the `childID` the next delegation will mint.
- **Action**: dispatch that delegation.
- **Expected outcome**: the collision is detected and surfaced — the delegation either fails loudly or mints a fresh id; under no circumstance does the child silently adopt the pre-existing session's transcript, meta, owner or stats.
- **Category**: Edge Case

---

## Assumptions

- The `#576–#588` fix wave is already an ancestor of this branch (`0ee87fbe` reachable from the ADR commits), so W22's test inversions do not collide with concurrent edits. ADR-057 v4 operator decision 2 removed the sequencing gate; this remains an integration consideration, not a blocker.
- Greenfield holds: no session written before the cutover needs a reader for a fused `meta.json`, and no config migration is required.
- CI is the authority for Go test and build results. This spec assumes no implementer runs the full Go suite in the dev pod.
- `UnifiedMeta` is not a wire type in its own right — the REST/WS payloads derive from it — so W23 requires no `contracts/` change. W5 does, and follows Constraint #8's 5-step pipeline.
- The 64-shard constant is chosen to match the in-house precedent (`pkg/session/lifecycle_lock.go:17`, `pkg/entity/lock.go:12`), not to bound throughput.
- Windows has no cross-process file locking anywhere in the file-store family (`pkg/fileutil/flock_windows.go` is a no-op). W15's cross-process assertions are POSIX-only, matching `pkg/entity/store_crossprocess_test.go`'s `//go:build !windows` gate. This is an accepted, documented limitation, not a gap this spec closes.
- **Out of scope**: plan cancellation (D9), throttle unification (ratified cut, W17 excepted), `migrateLegacy`/`writeUnifiedMetaDirect`, and any fix to the pre-existing `rate_limit`/`replay_done` frame gaps.

## Clarifications

### 2026-08-03

- Q: Should the transcript visibility filter be scoped or deleted? → A: **Deleted** at all four sites. Operator decision 1 (greenfield) lifted the no-migration constraint v3 was designing around. Historical chats surfacing previously-hidden delegate narration is accepted (R-16).
- Q: Does this wait for the `#576–#588` wave to close? → A: **No.** Operator decision 2 removed the gate; this is bug resolution and simplification, and the wave already landed.
- Q: Strict direct-parent ownership, or an ancestor walk? → A: **Ancestor-chain walk**, depth-bounded (operator decision 3). It preserves root-over-subtree control and removes the sibling/cousin leak — better than both options v2 offered.
- Q: Stripe the store lock, or move the fsyncs out of it? → A: **Stripe it** — 64 shards on the in-house pattern, plus a narrow cache-only mutex, one-directional lock order, no fixed concurrency cap (operator decision 4).
- Q: How many files does `meta.json` become? → A: **Four** — identity/lifecycle, statistics, goal, loop. The boundary is the *writer*, not the reader (operator decision 5).
- Q: Which writes get throttled? → A: **Only the per-token counter path.** Every event-driven write (goal, loop, status, title, owner, workspace) stays immediate, because they are control flow, not display (operator decision 6).
- Q: Does throttle unification come along? → A: **No**, ratified unchanged, with the single exception of the ungated root-level fan-out (W17). D12's write-cadence throttle is unrelated despite the shared word (operator decision 7).
