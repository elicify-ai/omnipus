# Handover — `feature/plan-swimlane-board`

**Written:** 2026-07-29 · **Branch HEAD:** `a41d5df4` (pushed, tree clean, 56 commits since `722ab21b~1`)

---

## 1. Read this first — the defect shape that dominates this branch

Nearly every real bug found here is the **same shape**: *something reports success it never achieved.* Nine instances shipped with passing tests. The tests passed because they asserted **the mechanism** (a map entry was written, a dispatch was called, a prompt contained a word) rather than **the property** (the correction applied, the agent is usable, the message reached the right agent).

Two consequences for how you should work:

- **A green test is not evidence.** Mutation-test every fix — revert it, prove the test goes red, restore. Several agents caught their *own* vacuous tests this way, and one shipped a fix whose test stayed green 5/5 with the fix removed.
- **A red test is not evidence either.** Three separate controls here *misreported* their own failure: a WS test blamed "per-agent session lookup" for a timer bug in itself; a concurrency test blamed "starvation" for a socket error; CI's flake filter reported "failed twice" for two *different* tests failing once each — which sent an investigation chasing a regression that never existed for 49 runs.

Related durable sub-pattern: **a partial object meeting replace semantics**. Hit 3× (plan bounds, agent MCP bindings, two latent sites). It stays invisible while the client happens to send every field that exists, then arms itself the moment someone adds a field.

---

## 2. Current CI state

**Worker:** `ci-omnipus-2` (private, `performance-8x`/16GB/`sin`). The shared `ci-omnipus` belongs to another session — **do not use it**.

```bash
fly ssh console --app ci-omnipus-2 -C "/cache/runci.sh <sha> all"
```

**On `a41d5df4`: all 10 main gates PASS** (`cli-verb-guard`, `npm-ci`, `gofmt`, `go-build`, `go-vet`, `golangci-lint`, `verify-contracts`, `typecheck`, `vitest`, `go-test`). **7 of 8 e2e shards pass.**

### Reading a verdict — three traps, all have bitten this project

1. **Stale checkout.** Always confirm the `HEAD: <sha>` line matches what you pushed.
2. **Wrapper exit code is not the verdict.** `fly ssh -C` reports the SSH wrapper's code. It reported **exit 0 over a failed deploy** in this session. Parse the per-gate `-> exit N` lines.
3. **Missing browser (e2e).** Every test failing in 4–6 ms = no browser started, not a regression.

Also: **check `/cache/runci.sh`'s md5 against the repo copy before trusting anything.** The shared worker's copy was found 80 lines stale with the `flock` mutex entirely absent, so concurrent runs `git reset --hard` the checkout underneath each other. Full detail in `deploy/ci-worker/CLAUDE.md`.

---

## 3. BLOCKS GREEN (2)

### 3.1 Test harness cannot boot a gateway within 15s under load

```
Error: http://localhost:43765/health did not return 200 within 15000ms
       at tests/e2e/fixtures/setup.ts:185
```

Fails at **0 ms**, three times — the test body never runs. Ruled out: not the shared helper I changed, and nothing in this epic touched `hot-reload.spec.ts`.

**Third sighting of one class.** The Go-side twin (`TestConcurrentSessions_FiveSessions_SameAgent`) was diagnosed in detail: boot costs **0.22 s p50**, and only **0.75 s at 8:1 CPU oversubscription** — 20× under the budget. The deadline is **pure wall-clock**, so a multi-second host freeze consumes the entire budget with no work done, then reports something undiagnosable.

**Recommended fix** (`pkg/agent/testutil/gateway_harness.go` + `tests/e2e/fixtures/setup.ts`): require N consecutive *failed probes* rather than a wall-clock deadline, and log elapsed-vs-attempts on failure. The Go harness also never checks `gw.bootErr` inside the loop, so a fast boot failure still burns the full 15 s.

### 3.2 `TestReplay_ToolCallPairsEmitted`

Fails the contended run, passes isolated. **Undiagnosed.** Only visible because `510d337a` fixed the flake filter to name the failing tests instead of printing nothing.

Treat as a real bug (every "flake" investigated this session had a root cause; only one turned out to be genuinely environmental).

---

## 4. REAL BUGS, NOT FIXED (6)

Ordered by user impact.

### 4.1 `Reload` can crash the gateway — same bug as the one just fixed

`pkg/channels/manager.go` (~`:1616`). `Reload` cancels the dispatcher then tears down workers for removed channels **without waiting for the dispatchers to exit** — identical to the `StopAll` bug fixed in `f5cfa241`. Send-on-closed-channel **panics**.

The detector didn't catch it only because the integration test doesn't reload. **The new race job may now surface it.**

Fix shape is in `f5cfa241`: `asyncTask` already carries the `WaitGroup`. **The trap:** `dispatchLoop` takes `m.mu.RLock`, so waiting under the write lock **deadlocks**. `StopAll` releases the lock for the wait deliberately — `Reload` holds it for several other reasons, which is why this needs its own change.

### 4.2 [#571](https://github.com/elicify-ai/omnipus/issues/571) — agent create/update restarts every service

**The operator asked for this to be fixed, not filed.** Design + both traps are in an issue comment; nothing needs re-deriving.

Short version: ADR-054 split agents into per-file entity storage (fixing *parallel* agent creation), but the **read** path still rebuilds the whole registry via a config reload. Adding one agent restarts channels, cron, schedulers, plan engine — up to ~60 s under load.

Safe shape: `NewAgentInstance(...)` → `registry.UpsertAgent(...)` → **re-run `registerSharedTools`** (it iterates all agents, so wiring parity is *by construction*, not by checklist).

- **Trap 1:** `AgentRegistry` caches `resolver *routing.RouteResolver`, built once at `registry.go:53`, **no setter**. A bare upsert leaves the agent visible to `GetAgent` but invisible to routing/default-agent — the ADR-037 anti-pattern.
- **Trap 2:** the sequence isn't atomic vs a concurrent `GetAgent`/`ResolveRoute`.
- **Acceptance test is the deliverable:** prove an upserted agent is *indistinguishable* from a rebuilt one (same tools, same resolved policy, resolvable by `POST /tasks`, workspace `core_team`, routing, `GetDefaultAgent`). A test asserting "it's in the map" passes against every broken variant.

### 4.3 Stop in the first ~1–3 seconds is a silent no-op

No latch for a cancel arriving **before** its turn registers. `RequestCancel` → nil hook → emits `was_fired:false` → returns; the turn starts moments later and runs to completion. **Proven:** two Stop clicks, full 11,788-token completion, and the orphan watchdog's `ClaimCancel` succeeded 22 s later on the same session.

Needs a TTL/scoping decision (a latch that outlives its turn would cancel the *next* one), which is why it wasn't inlined.

### 4.4 Model picker silently resets the user's choice

`src/components/chat/composer/ModelPicker.tsx:73-89`. Guards are "same seed key" plus a `__pending` carve-out — **no explicit-vs-derived discriminator**. Any change to `activeAgentId` re-seeds the user's per-turn model to the new agent's default, and that value goes on the wire.

This is the twin of the agent-picker clobber fixed in `5157e378`; **pre-existing, not introduced by it**. Same fix shape: a `modelSelectionSource`, or have `onPickerChange` claim the current `lastSeedKey`.

### 4.5 `StopTask` renders a successful stop as a 409

`pkg/gateway/rest_tasks.go` — `handleTaskStop` has no partial-success branch, discards `updated` on error, then re-reads the task (by then `failed`) and answers 409. `handlePlanStop` already has the branch it needs.

### 4.6 Gateway tests silently refuse plan turns

`mustAgentLoop`'s workspace-membership seed reads `cfg.Agents.List` verbatim while the registry **lowercases** agent IDs, so the lookup key is never written. Every plan wake turn in `pkg/gateway` is refused at the ADR-046 P1 gate *before* the LLM call:

```
WRN plan wake turn ended with an error
    error="agent is not a member of any workspace; turn refused: agent_id=01jxtestplansagent0000001"
```

Any gateway test that believes it exercises a real turn for a mixed-case agent ID does not.

---

## 5. DECISIONS, NOT BUGS (4)

- **Hidden tools: search instead of list** *(operator's idea, endorsed)*. Today lazy tools are *listed* in a compact index — fine at 24, heavy at ~450 (15 MCP servers). Searching instead stops the cost scaling with servers connected. Not needed for safety: the counting bug is fixed (`4ebf2f1b`). **Do not** make the 17 hot tools lazy — costs an extra round-trip nearly every turn.
- **Tool-choice bias.** `create_task` is `ManifestFull` (always visible); `delegate` is `ManifestLazy`. The model reaches for the slower task route because it's the one it can see — measured **304 s vs 20–80 s**. Assessed as unintended. Options: promote `delegate` to Full, or demote `create_task` to Lazy.
- **The `ask` on `run_task` is unenforceable.** Denied → `CheckQueuedTasks` (~60 s heartbeat drain, `task_executor.go:2171`) dispatches it anyway, never consulting the gate. The operator confirmed the *prompt* is correct (it's a tool-call approval); this is the separate half.
- **Two shared test helpers write to the real `~/.omnipus`.** `ensureTestWorkspaceMembership` (`test_agent_loop_helper_test.go:130`) uses `config.OmnipusHomeDir()`, so every `mustAgentLoop` parses every workspace file in the developer's actual home. Cross-process shared global; scales with accumulated state.

**Also open, pre-existing (tracked separately):** Windows cross-process locking for `pkg/entity`; toasts invisible behind slide-overs; `TestConcurrentSessions_TranscriptPersisted` doesn't test persistence (doc corrected, assertion never written).

---

## 6. CLOSED BY OPERATOR DECISION — do not re-raise

- **CLA `Co-Authored-By: Claude` lines** on `4a7cc392`, `023a27c7`, `afb50950`, `5d907d17` — **permanent accepted exception.**
- **`list_tasks_in_workspace` scoping** — the rule is "an agent sees tasks it created *and* tasks assigned to it". Already exactly what ships. Human-created tasks are correctly excluded.
- **`run_task` approval prompt** — correct as-is; it's a tool-policy `ask`, not a run approval.

---

## 7. What was fixed here (context for regressions)

**9 product bugs**, all "reported success it hadn't achieved":

| commit | fix |
|---|---|
| `94a0356d` + `49a63817` + `911bb103` | agent created → `201` → unusable. Three layers: dropped reload, clobbered pending flag, and a wait that **returned success after 5 s against a ~60 s worst case** |
| `89b62da9` | permission grant silently overruled by the global ceiling (**third instance**; a test asserted the bug as intended behaviour) + approvals now leave a transcript entry |
| `5157e378` | agent picker reverted the user's choice; early slash command sent as chat |
| `4ebf2f1b` | context budget charged the whole tool catalog instead of what's sent (**25× over**; 15 MCP servers would have driven history negative) |
| `60bc0446` | data race: boot sweep read a pointer assigned 5 lines after its goroutine started |
| `f5cfa241` | data race: shutdown closed worker queues while senders were active — **panics** |
| `1ccf2368` | `list_tasks` returned every task in every workspace to a caller with no principal |
| `00ec06f4` | a tool-policy toggle wiped the agent's MCP bindings (fired on autosave, unrecoverable from the UI) |

**Infrastructure:**

- `8a399b46` — removed a blanket `//go:build !cgo` from 312 files. **The race detector had never run on `pkg/gateway`**, the tag protected nothing (all three documented rationales refuted), and **CI pinned `CGO_ENABLED=0` everywhere so the documented gate never ran**. Both races above were found within minutes of it working.
- `7c043e60` — `pkg/gateway` + `tests/integration` added to the CI race job (verified locally race-clean first; timeout 240 s → 600 s).
- `510d337a` — flake filter no longer calls two different tests failing once each "failed twice"; now prints assertion text and names absorbed flakes.

---

## 8. Ground rules (from `CLAUDE.md`, learned the hard way here)

- **Build tags `goolm,stdjson` are mandatory.** Without them `pkg/channels/matrix` won't compile and you get a misleading "build constraints exclude all Go files".
- **`golangci-lint` needs `CGO_ENABLED=0`** or you get a bogus `undefined: restAPI` cascade.
- **Never run the full Go suite locally** — OOMs this pod. Narrow `-run`, `-p 1`. `go build ./...` is fine; `go test ./...` is not.
- **Commits:** author *and* committer must be `Daniel Piatkowski <10800669+daniel-piatkowski-ai@users.noreply.github.com>`, **no agent `Co-Authored-By` trailers** (CLA gate hard-fails).
- **The git index is shared with sibling agents.** A bare `git commit` swept a sibling's staged deletion this session. Use `git add <paths> && git commit -F msg -- <paths>`; note `git commit -- <path>` commits the **working tree**, not the index.
- **Never merge to `main` without human approval** — no `--admin`, no `--auto`.
- **Code intelligence is GitNexus, not graphify.** `graphify` does not exist here.
