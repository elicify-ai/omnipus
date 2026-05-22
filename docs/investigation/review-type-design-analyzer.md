# Type Design Review — feature/iframe-preview-tier13 (5-bug-fix work)

**Summary.** Four new types ship in this branch: `sessionWorker`, `AdmissionController`, `SandboxApplyOptions` (extended with `GetEnv`/`Stderr`), and the `SandboxMode`/`SandboxProfile`/`PortRange` value types. Overall the designs are competent — invariants are mostly enforceable, encapsulation is appropriate for in-package use, and the new enum types in `pkg/config/sandbox.go` are a small but exemplary improvement (typed enums with `UnmarshalJSON` that reject typos). The principal concerns are: (1) `sessionWorker.inTurn` + `processTurn` re-entrancy semantics are not type-enforced and depend on call-site discipline, (2) `AdmissionController`'s threat model (subagent spawn protection) is undermined by `OnTurnStart` being called *inside* the worker after the cap check has already passed, (3) `sessionWorker.lastActive` is written but never read — dead state, and (4) `AdmissionController` lacks a `TryAdmit` atomic test-and-increment which leaves a TOCTOU window in `loop.go:1555-1580`.

---

## Type: `sessionWorker` — `pkg/agent/session_worker.go:32`

### Invariants identified
- `scope` and `agentID` are immutable after construction (no setter; comment at L34-L40 documents this).
- Exactly one goroutine reads `inbox` (the `runLoop` goroutine started right after `newSessionWorker`).
- `inTurn` is true only while `processTurn` is executing on the worker's goroutine.
- `done` is closed once and only once — by the single `defer close(w.done)` at L131.
- `parent != nil` is assumed throughout but never asserted at construction.
- Self-removal: when `runLoop` exits, the worker removes itself from `parent.sessionWorkers` (L132).

### Ratings
- **Encapsulation: 4/5.** The struct is lower-case package-private; only `newSessionWorker`, `enqueue`, `runLoop`, `processTurn` are referenced externally (within the package). The reach into `parent.*` (15+ methods) is the leakiest part — `processTurn` calls `al.admission`, `al.processMessage`, `al.buildContinuationTarget`, `al.publishResponseIfNeeded`, `al.pendingSteeringCountForScope`, `al.Continue`, `al.channelManager.InvokeTypingStop` — but per the brief, we are not flagging this as an interface candidate because the concrete type already works in tests.
- **Invariant expression: 3/5.** The "inTurn implies turn-in-progress" invariant is documented in prose (L62-L66) but not type-enforced — nothing prevents an external caller from doing `w.inTurn.Store(true)`. The `cancel != nil` invariant is held only by construction; if a future refactor zeros it, the type would not catch it.
- **Invariant usefulness: 4/5.** The atomic `inTurn` flag actually serves a real purpose (routes same-scope messages to the steering queue vs the inbox) and the test in `steering_test.go` exercises this. `done`'s wait-with-deadline pattern in `stopSessionWorkers` (loop.go:1603-1609) is a real lifecycle primitive.
- **Invariant enforcement: 3/5.** Two concrete gaps:
  1. **`lastActive` is write-only.** `pkg/agent/session_worker.go:59,92,154` store into it but no code (production or test) reads it. The idle-timeout path uses `idleTimer` (a `time.Timer`) at L134; `lastActive` was either a leftover from an earlier design or unfinished observability scaffolding. **Drop the field** or wire up a getter + `/api/v1/sessions/workers` debug endpoint (whichever the operator actually needs).
  2. **`inTurn` race on Close().** `Close()` calls `w.cancel()` (loop.go:1600) while `processTurn` may be running. The `defer w.inTurn.Store(false)` (session_worker.go:181) will fire eventually, but if a new `enqueue` lands between the cancel and the defer, it will see `inTurn=true`, route to the steering queue, and the queued steering will be orphaned because no further turns will execute on this scope. Severity: low (Close is shutdown-only) — but worth a comment that explicitly disclaims this scenario.

### Strengths
- Clear, defensive `enqueue` fallback path: in-turn → steering, else → inbox, else → log-and-drop (L101-L124). No silent drops.
- Idle-timer reset pattern (L156-L163) handles the timer-fired-but-not-drained edge case correctly.
- `processTurn`'s nested `defer` for the response-publish guard (L196-L206) preserves the original `Run()` semantics including panic-safe publish.
- Construction does NOT start the goroutine — `newSessionWorker` is pure, and the caller (loop.go:1581) explicitly does `go w.runLoop()`. This is the right separation (testability + lifecycle control).

### Concerns
- **`pkg/agent/session_worker.go:59,92,154` — `lastActive` is dead state. Severity: low-medium.** No reader. Either delete or expose. Suggest: delete for v0.1; if observability needs land in v0.2 add a typed `Snapshot()` method that returns `{Scope, AgentID, LastActive, InTurn}` rather than exposing the atomic directly.
- **`pkg/agent/session_worker.go:70` — `parent *AgentLoop` is not nil-checked in `processTurn`.** `enqueue` defensively checks `if w.parent != nil` at L108, but `processTurn` does `al := w.parent` at L175 and then unconditionally dereferences. Inconsistent. Severity: low (`newSessionWorker` is package-private and the one caller at loop.go:1579 always passes `al`), but make the contract explicit: either add a constructor precondition or remove the defensive check in `enqueue`. Recommend: drop the `enqueue` check since `parent` is mandatory.
- **`pkg/agent/session_worker.go:181` — `defer w.inTurn.Store(false)` runs before the outer publish-defer.** Go LIFO defer order means the inner `inTurn.Store(false)` (declared at L181) fires LAST, which is what you want — but the comment at L177-L181 doesn't explain the ordering interaction. Severity: cosmetic.
- **`pkg/agent/session_worker.go:208-210` — `OnTurnStart` happens *inside* the worker after admission has already approved.** This means the admission counter rises only when the work actually begins, not when the worker is spawned. For the "subagent spawn" threat (which spawns a worker without inbound bus traffic), the counter never increments — `AdmissionController` does not see subagent work. See AdmissionController concerns below.

### Proposed fixes
1. Delete `lastActive` field + `Store(&now)` at L92 and L154. If observability is desired later, replace with a typed snapshot accessor.
2. Make `parent` non-nil a hard precondition: `if parent == nil { panic("nil parent") }` in `newSessionWorker`, then drop the L108 nil check.
3. Add a `// In-turn race during Close() is documented at <link>; steering enqueued in this window is intentionally orphaned because the gateway is shutting down.` comment near L181.

---

## Type: `AdmissionController` — `pkg/agent/admission.go:17`

### Invariants identified
- `softCap > 0` (enforced by `newAdmissionController` at L24-L29).
- `softCap` is immutable after construction (no setter).
- `activeTurns` is a non-negative counter (relies on `OnTurnStart`/`OnTurnEnd` pairing).
- `ShouldAdmit() == (activeTurns < softCap)` — a snapshot read; no atomic test-and-set.

### Ratings
- **Encapsulation: 5/5.** Six small methods, all exported, none expose the internal `atomic.Int64`. Clean.
- **Invariant expression: 3/5.** The "Start and End must be paired" invariant exists only as a doc comment (L38-L48). Nothing prevents a caller from calling `OnTurnEnd` without a prior `OnTurnStart`, which would push the counter negative and silently widen the cap. There is no `defer`-style scope guard returned from `ShouldAdmit`.
- **Invariant usefulness: 2/5.** Per the brief: *"is the soft-cap actually preventing the threats it claims to (subagent spawn, user-message throughput)?"* The current wiring (loop.go:1555 calls `ShouldAdmit`; session_worker.go:209 calls `OnTurnStart`) protects only the **user-message throughput** path — i.e. inbound bus messages routed to a worker. The **subagent spawn** path (a subagent created via `system.spawn_agent` or the task executor) does NOT route through `Run()`'s inbound dispatch — it calls into `processMessage` directly via other paths — so the cap does not gate subagent work. The comment in admission.go:12-16 advertises "soft-cap gate for concurrent session workers" without distinguishing the two threats; the test file `pkg/agent/session_worker_test.go:240` (TestSessionWorker_AdmissionRejection) tests only the user-message path.
- **Invariant enforcement: 2/5.** Two concrete gaps:
  1. **TOCTOU between `ShouldAdmit()` and `OnTurnStart()`.** `loop.go:1555` checks `ShouldAdmit()`, then `loop.go:1581` does `go w.runLoop()`, then *eventually* (after the inbox receive, after `processTurn` is called) `session_worker.go:209` does `OnTurnStart()`. Two concurrent inbound dispatches at cap-1 can both pass the check, both spawn workers, and both increment to cap+1. The window is microseconds but it's real.
  2. **No matching guard on `OnTurnEnd`.** Caller can underflow.

### Strengths
- Simple, testable, minimal surface.
- The `newAdmissionController(0)` default-injection pattern (`0 → NumCPU() * 4`) is a clean way to defer cap configuration while keeping the type immutable.
- Five focused unit tests in `pkg/agent/admission_test.go` cover the obvious paths.

### Concerns
- **`pkg/agent/admission.go:34-36` — `ShouldAdmit` + separate `OnTurnStart` is racy.** Severity: medium. The TOCTOU lets the counter overshoot the cap by O(num inbound dispatchers). In production there's exactly one inbound dispatcher (the `for` loop in `Run()`), so the practical race is limited — but a future refactor that adds parallel dispatch would silently break the cap.
- **`pkg/agent/admission.go:46-48` — `OnTurnEnd` can underflow.** Severity: low. The counter is `int64` so wrap is benign, but `ActiveTurns()` returning a huge number to observability would be confusing.
- **`pkg/agent/admission.go:12-16` — doc comment overpromises threat coverage.** Severity: medium for spec/docs honesty, low for runtime. Subagent spawn is not gated.

### Proposed fixes
1. **Atomic test-and-increment.** Replace `ShouldAdmit` + `OnTurnStart` with a single `TryAdmit() (admitted bool, release func())`:
   ```go
   func (a *AdmissionController) TryAdmit() (bool, func()) {
       if a.activeTurns.Add(1) > int64(a.softCap) {
           a.activeTurns.Add(-1)
           return false, func() {}
       }
       return true, func() { a.activeTurns.Add(-1) }
   }
   ```
   Closes the TOCTOU window and gives the caller a `defer release()` idiom that can't be unpaired.
2. **Honest doc.** Update L12-L16 to read *"Phase 1: gates inbound user-message dispatch only. Subagent spawn and task-executor dispatch paths are NOT gated — tracked in #175."*
3. Keep the old `ShouldAdmit`/`OnTurnStart`/`OnTurnEnd` for the existing test surface, but mark them `// Deprecated: use TryAdmit.` if the cap goes anywhere near production.

---

## Type: `SandboxApplyOptions` (extended) — `pkg/gateway/sandbox_apply.go:59`

### Invariants identified
- `Cfg != nil` is not required (the code guards `if opts.Cfg != nil` at L239, L327, L356).
- `GetEnv == nil` is permitted and defaulted to `os.Getenv` (L226).
- `Stderr == nil` is permitted and defaulted to `os.Stderr` (L229).
- `Backend == nil` is permitted and defaulted to `sandbox.SelectBackend()` (L260-L265).
- "Defaults are mutated in place" — `applySandbox` mutates the caller's struct when defaults fire. Surprising for a *value*-passed struct but harmless because the struct is constructed at the call site.

### Ratings
- **Encapsulation: 4/5.** All fields exported; this is an options struct, not a domain entity. The exported fields are appropriate for an in-package boot helper.
- **Invariant expression: 4/5.** The `GetEnv func(string) string` field is the right shape — it's a tiny, well-known, single-method "interface" expressed as a function type. An `EnvProvider` interface would be more ceremony for no gain. This is idiomatic Go (cf. `http.HandlerFunc`).
- **Invariant usefulness: 4/5.** The test files `pkg/gateway/sandbox_apply_test.go:110,146,198,...` and `boot_sandbox_mismatch_test.go:49,98` all use `GetEnv: func(string) string { return "" }` or similar — the indirection earns its keep.
- **Invariant enforcement: 3/5.** Default-injection is implicit (mutates `opts`). A caller that reads `opts.GetEnv` after calling `applySandbox` would see a different value than they passed in. Minor footgun.

### Strengths
- Per the brief, choosing `func(string) string` over an `EnvProvider` interface is the right call for a single-method hook. Avoids the "interface with one method" anti-pattern.
- The struct's options pattern allows the boot caller in `gateway.go:536` to pass a single value with named fields.

### Concerns
- **`pkg/gateway/sandbox_apply.go:226-231` — in-place default mutation.** Severity: low. Prefer reading defaults into local vars (`getEnv := opts.GetEnv; if getEnv == nil { getEnv = os.Getenv }`) over mutating `opts`. Avoids the "callers see modified state after the call" subtlety.
- **No constructor.** Severity: cosmetic. For 5 fields with sensible defaults, this is fine.

### Proposed fixes
1. Replace L226-L231 in-place mutation with local variables; reference `getEnv`/`stderr` locally throughout the function.

---

## Type: `SandboxMode`, `SandboxProfile`, `PortRange` — `pkg/config/sandbox.go:45-170`

These deserve a callout as well-designed:

### Ratings (all three)
- **Encapsulation: 5/5.** Typed enums with `UnmarshalJSON` validation that rejects typos at load time.
- **Invariant expression: 5/5.** Illegal modes (`"enfroce"`) literally cannot be decoded. `PortRange.IsZero()`, `.Min()`, `.Max()`, `.Validate()`, `.Contains()` give a complete, minimal interface.
- **Invariant usefulness: 5/5.** Catches a real class of config bug (silent typo → permissive default) at the input boundary.
- **Invariant enforcement: 5/5.** `UnmarshalJSON` is the only construction path from config, and it switches on the enumerated values.

### Concerns
- **`pkg/config/sandbox.go:95-98` — `IsValid()` on a pointer receiver.** `func (p *SandboxProfile) IsValid() bool` is unusual for a value-typed enum. Either use a value receiver `func (p SandboxProfile) IsValid()` (consistent with `PortRange.IsZero()` at L80) or document why pointer was chosen. Severity: cosmetic.
- **`pkg/config/sandbox.go:100` — `String()` on pointer receiver.** Same. Idiomatic Go uses value receivers for stringers on string-backed types.

### Proposed fixes
1. Change `SandboxProfile.IsValid` and `SandboxProfile.String` to value receivers (`func (p SandboxProfile) ...`).

---

## Type: `evals/scenarios/capability/concurrent-sessions.yaml`

The YAML is consumed by `evals/cmd/eval-runner/main.go:39` `type Scenario struct {...}`, which uses explicit `yaml:` tags and has a `validate()` step. The new file uses the same key set (`id`, `category`, `agent_id`, `prompt`, `expected_tools`, `forbidden_tools`, `max_turns`, `rubric`) as the existing scenarios. **Well-typed.**

### Concerns
- **`evals/scenarios/capability/concurrent-sessions.yaml:5-12` — the file documents that the eval harness "may not have a concurrency primitive" and will be marked SKIP.** A YAML file that exists primarily as documentation of expected behavior is acceptable but misleading. Severity: low. Either:
  1. File a tracked issue for the harness primitive and link it in the comment, or
  2. Move the documentation to `docs/investigation/bug-3-concurrent-sessions.md` and gate the YAML behind the harness capability.

---

## Test-mock shape compatibility

**`pkg/agent/mock_provider_test.go:11` `mockProvider`** — shape-compatible with `providers.LLMProvider` (file: `pkg/providers/types.go:24`). Implements `Chat(ctx, []Message, []ToolDefinition, model, opts) (*LLMResponse, error)` and `GetDefaultModel() string`. No race issues — stateless.

**`pkg/agent/session_worker_test.go:360` `blockingMockProvider`** — shape-compatible. Honors `ctx.Done()` in select (L373-L375). This is the *correct* shape for a concurrency test mock: production providers must respect context cancellation, and the test verifies that. **Good design.**

**`pkg/agent/steering_test.go:517` `lateSteeringProvider`** — shape-compatible. Uses `sync.Mutex` to protect `calls` and `secondCallMessages`. Does NOT honor `ctx.Done()` in the `<-p.releaseFirstCall` path at L540 — a context cancel would hang the test until `releaseFirstCall` is closed. **Minor concern**, severity: low. Add `select { case <-p.releaseFirstCall: case <-ctx.Done(): return nil, ctx.Err() }` for consistency with `blockingMockProvider`.

**No `concurrentMockProvider` exists** in the codebase — referenced in the task description but not present. `mockProvider` itself returns instantly, so the test relying on concurrency uses goroutines and counts replies (`session_worker_test.go:192-235`) rather than blocking.

---

## Contract-first wire-type compliance

- **`pkg/gateway/websocket_replay_order_test.go`** — references `generated.WsFrameTypeToken` at L358, L397. **Compliant** — uses the generated contract type.
- **`tests/integration/replay_ordering_test.go:265-267`** — declares an inline `struct { ID string \`json:"id"\` }` for decoding a session-create response. **Borderline.** This is a test helper for parsing one field; the production contract for `POST /api/v1/sessions` is in `contracts/openapi.yaml`. A strict reading of hard-constraint #8 would say *"use the generated `CreateSessionResponse` type"*. Severity: low — it's a test-only ID extraction, but ideally use `generated.CreateSessionResponse` or similar.
- **`tests/integration/helpers_test.go:35-37, 180-182`** — inline `struct { Stream bool \`json:"stream"\` }` and `struct { Type string \`json:"type"\` }`. These are decoding LLM-provider responses and WS frame discriminators in test scaffolding, NOT the gateway↔SPA boundary. **Compliant** (out of scope for hard-constraint #8).

---

## Well-designed types — hold up as examples

1. **`SandboxMode` / `SandboxProfile`** in `pkg/config/sandbox.go:48-128`. Typed enum + `UnmarshalJSON` that rejects unknown values. Empty-string is explicitly handled. This is the right way to add new string-valued config knobs going forward — every future config enum should follow this template.
2. **`PortRange`** in `pkg/config/sandbox.go:177-220`. Five-method interface (`IsZero`, `Min`, `Max`, `Validate`, `Contains`) over a `[2]int32` array. Compact, total, with the zero-value-as-sentinel pattern made explicit.
3. **`blockingMockProvider`** in `pkg/agent/session_worker_test.go:360`. Honors `ctx.Done()` correctly — every other concurrency mock in the package should match this shape.
4. **`SandboxApplyOptions.GetEnv`** as `func(string) string`. Demonstrates that a function-typed field beats a single-method interface for hook injection. Generalizable.

---

## Findings table (severity-ranked)

| File:line | Severity | Finding | Fix |
|---|---|---|---|
| pkg/agent/admission.go:34-48 | medium | TOCTOU between `ShouldAdmit` and `OnTurnStart` lets cap overshoot under parallel dispatch | Replace with `TryAdmit() (bool, func())` atomic test-and-increment |
| pkg/agent/admission.go:12-16 | medium | Doc claims "soft-cap gate for concurrent session workers" but only gates inbound bus dispatch — subagent spawn bypasses | Tighten doc; track subagent gating in #175 |
| pkg/agent/session_worker.go:59,92,154 | low-medium | `lastActive` is write-only dead state | Delete the field, or add a `Snapshot()` accessor when observability lands |
| pkg/agent/session_worker.go:108 | low | `parent` nil-check in `enqueue` but assumed non-nil in `processTurn` — inconsistent | Make non-nil a constructor precondition; drop the L108 check |
| pkg/gateway/sandbox_apply.go:226-231 | low | In-place mutation of `opts` defaults | Use local vars instead |
| pkg/config/sandbox.go:95-100 | cosmetic | `SandboxProfile.IsValid`/`.String` use pointer receivers | Change to value receivers |
| pkg/agent/steering_test.go:540 | low | `lateSteeringProvider.Chat` ignores `ctx.Done()` | Add a `select` with `ctx.Done()` |
| tests/integration/replay_ordering_test.go:265-267 | low | Inline anonymous struct for `CreateSessionResponse.id` | Use `generated.CreateSessionResponse` |
| evals/scenarios/capability/concurrent-sessions.yaml:5-12 | low | Documents harness gap; eval is effectively a SKIP marker | File harness-primitive issue or move doc to investigation/ |
