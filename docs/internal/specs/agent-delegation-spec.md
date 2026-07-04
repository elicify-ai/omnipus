# Feature Specification: `delegate` — Unified Agent Delegation Tool + Tool-Approval Grant Inheritance

> Tool name resolved 2026-07-04: `delegate` (see Clarifications). Replaces `spawn`, `run_subagent`, `check_spawn_status`.

**Created**: 2026-07-04
**Status**: Draft
**Input**: Session design review (`tool-consolidation-design.html`, approved) + [ADR-036](../architecture/ADR-036-consolidate-shell-and-subagent-tools.md) §3.2, §3.3 + commit `d0f65482` (`fix(gateway): scope tool 'Always Allow' by (session, agent) + inherit on delegation`, already on `origin/hotfix/v0.1.1`, independently verified in-session)
**Companion specs**: [`bash-tool-spec.md`](./bash-tool-spec.md), [`async-notifier-spec.md`](./async-notifier-spec.md)

> **Implementation precondition (added 2026-07-04, 7-reviewer gate MAJ-002):** before starting, verify the working tree includes commit `d0f65482` (`git log --oneline | grep d0f65482`). At the time this spec was drafted, the local checkout was two commits behind `origin/hotfix/v0.1.1` and did not include it (`pkg/security/approvalgrants.go` does not exist there) — if absent, fetch and check out `origin/hotfix/v0.1.1`'s actual tip rather than branching from local HEAD.
>
> **7-reviewer gate verdict (2026-07-04): REVISE, now fixed below.** No CRITICAL findings. Two MAJOR findings claimed factual errors in this spec (`SubTurn.MaxDepth` default, `Inherit`'s transitivity) — both were independently re-verified against the real code during the fix pass and turned out to be **correct as originally written**; see Clarifications for the re-verification record. That re-verification surfaced one genuine, separate bug — filed as [#477](https://github.com/elicify-ai/omnipus/issues/477) — which is now **pulled into this spec's scope** (User Story 3, FR-D9) rather than left as a standalone follow-up.

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `pkg/tools/spawn.go`'s `SpawnTool` | replaces (merged) | Today's `spawn` — async delegation, calls `SubTurnSpawner.SpawnSubTurn(..., Async: true)` directly in a goroutine, bypassing `SubagentManager.tasks` entirely |
| `pkg/tools/subagent.go`'s `SubagentTool` | replaces (merged) | Today's `run_subagent` — sync/blocking delegation, calls the same `SpawnSubTurn(..., Async: false)` |
| `pkg/tools/spawn_status.go`'s `SpawnStatusTool` | replaces (merged), bug fixed | Today's `check_spawn_status` — reads `SubagentManager.tasks`, which `spawn` never writes to. **Confirmed dead code path in current production behavior**: checking on a `spawn`-created task always reports "no subagents have been spawned yet." |
| `pkg/tools/subagent.go`'s `SubTurnSpawner`/`SubTurnConfig` | extends | The shared underlying primitive both `spawn` and `run_subagent` already call — `Async bool` is the only differentiator, confirming the merge is low-risk |
| `pkg/agent/subturn.go`'s `spawnSubTurn` | extends | The actual sub-turn execution function; already contains the `ApprovalGrants().Inherit(...)` call (from `d0f65482`) that must continue to work unchanged once the tool identity above it is merged |
| `pkg/security/approvalgrants.go`'s `ApprovalGrantStore` | unchanged, verified | `IsAllowed`/`Record`/`Inherit`/`ClearSession` — session-scoped, (sessionID, agentID) keyed, fail-safe-by-default. Not modified by this spec — its correctness was independently verified in-session (see §"Verified Behavior" below). |
| `pkg/gateway/ws_approval.go`'s `wsApprovalHook.ApproveTool` | unchanged, verified | The call site that resolves policy FIRST (allow/deny short-circuit before any grant check) and only consults `ApprovalGrantStore.IsAllowed` when policy resolves to `ask` — this ordering is what makes grant inheritance safe against a child's own `deny` policy. |
| `pkg/tools.AsyncCallback`/`AsyncNotifier` (per `async-notifier-spec.md`) | calls | `delegate`'s async-completion delivery reuses this exact mechanism, unchanged. |
| `pkg/agent/loop.go`'s `enforceEdgeModeAndDepth`/`buildDelegationDenyChecker` | extends (added 2026-07-04, #477 pulled into scope, corrected understanding) | The REAL, operator-facing depth authorization gate — reads each delegation-graph edge's own `Depth` field (nil = inherit/no per-edge cap, >0 = per-edge onward-delegation cap) plus the raw `globalDepthCap := defaults.SubTurn.MaxDepth` (0 = "no constraint from this source" at THIS gate), and already computes the effective, tighter-of-both cap (`depthCap`) internally — but only uses it for an allow/deny decision, does not expose it to anything downstream. |
| `pkg/agent/subturn.go`'s `getSubTurnConfig`/`spawnSubTurn` | extends (added 2026-07-04, #477 pulled into scope, corrected understanding) | A SEPARATE spawn-time backstop, checking the SAME `ts.depth` counter (confirmed via `currentDelegationDepth(ctx)` reading `turnState.depth` — the identical field `enforceEdgeModeAndDepth` checks) but with DIFFERENT zero-value semantics: `if maxDepth <= 0 { maxDepth = defaultMaxSubTurnDepth /* = 3 */ }`. This backstop has NO knowledge of the delegation graph's per-edge `Depth` at all — it silently overrides (shortens) an operator's own explicit, deeper per-edge configuration whenever the global `SubTurn.MaxDepth` is left unset. This is the real bug, not merely a prompt-wording issue. |
| `pkg/agent/loop_env.go`'s `wireDelegationInjectors` | modifies (added 2026-07-04, #477 pulled into scope) | Currently reads the same raw `globalDepthCap := liveCfg.Agents.Defaults.SubTurn.MaxDepth` Gate A uses, and passes it straight to `buildDelegationContext` — internally consistent with Gate A's own semantics, but silent about Gate B's separate backstop. Must be updated to advertise the true EFFECTIVE cap (FR-D9). |
| `pkg/agent/delegation_context.go`'s `buildDelegationContext` | unchanged (context only) | Not modified itself — it correctly renders whatever `globalDepthCap` (and per-edge `Depth`) it's handed; the fix is entirely in what its callers compute and hand it. |

### Impact Assessment

| Symbol Modified | Risk Level | Direct Dependents | Indirect Dependents |
|----------------|------------|-------------------|---------------------|
| `pkg/tools/spawn.go`, `subagent.go`, `spawn_status.go` (merged into one tool) | MEDIUM | Every agent's tool registry, delegation-policy gate (FR-6.2, unchanged) | Any WS frame consumer expecting `subTurn_start`/`subagent_start` frame shapes — must not change |
| `pkg/agent/subturn.go`'s `spawnSubTurn` | LOW (call site only) | `delegate`'s `Execute`/`ExecuteAsync` methods | `ApprovalGrantStore.Inherit` call — **must be confirmed to still fire correctly regardless of which tool name invoked the delegation**, since `Inherit` is keyed on agent IDs, not tool identity |
| `pkg/gateway/ws_approval.go` | NONE (this spec adds tests, does not modify logic) | — | — |
| `pkg/agent/loop_env.go`'s `wireDelegationInjectors` (added 2026-07-04, #477) | LOW | The delegation system-prompt block every agent sees | None outside `pkg/agent` — purely a prompt-content fix, no wire/contract change |

### Relevant Execution Flows

| Flow Name | Relevance |
|-----------|-----------|
| Tool call → delegation-policy gate → `spawnSubTurn` → child turn runs → tool-approval requests during child turn → grant inheritance consulted | The full flow this spec's second half (approval inheritance) formalizes and adds missing regression coverage for. |
| Async completion → `AsyncNotifier.Notify` → new turn | Unchanged consumer of the mechanism specified in `async-notifier-spec.md`. |

### Cluster Placement

This feature belongs to the **agent-delegation / tool-approval** cluster — spanning both the tool-registry consolidation (shared cluster with `bash-tool-spec.md`) and the security-boundary work already shipped in `d0f65482`.

---

## Available Reference Patterns

> No `docs/reference/` directory applies (Go backend, not built from a reference-pattern library). The closest thing to a "reference pattern" here is `d0f65482` itself — an already-implemented, already-tested fix this spec formalizes and extends rather than designs from scratch.

---

## User Stories & Acceptance Criteria

### User Story 1 — One delegation tool, defaulting to background (Priority: P0)

Replaces `spawn`, `run_subagent`, `check_spawn_status`. Fixes the confirmed `check_spawn_status`/`spawn` disconnect as a direct consequence of the merge (one code path, one place status lives).

**Why this priority**: The disconnect is a live bug in production behavior today, not a hypothetical; fixing it is the primary deliverable, not a side effect.

**Independent Test**: Delegate a task with default parameters (async), then check its status by `task_id` — confirm the status reflects the real, in-progress or completed state, not "no subagents have been spawned yet."

**Acceptance Scenarios**:

1. **Given** an agent calls `delegate` with only `task` set, **When** the call returns, **Then** it returns immediately (async is the default) with a `task_id`, and the delegated turn continues in the background.
2. **Given** an agent calls `delegate` with `async: false`, **When** the delegated turn completes, **Then** the result is returned inline, blocking the caller until it's ready — matching today's `run_subagent` behavior exactly.
3. **Given** an async delegation is in progress, **When** the agent calls `delegate` with `action: status` and the correct `task_id`, **Then** the current state (running/completed/failed/canceled) is returned correctly — not "no subagents have been spawned."
4. **Given** any agent — including a main/orchestrating agent, not a role-restricted subset — **When** it calls `delegate`, **Then** access is governed solely by that agent's own delegation policy (trust set, modes, depth), never by which agent "kind" it is.

---

### User Story 2 — A subagent inherits its parent's tool approvals, but only where it would otherwise have to ask (Priority: P0)

Already implemented and independently verified in-session (`d0f65482`): when a parent has a tool always-allowed in the current session, a spawned child inherits that grant — but the inheritance is only ever consulted for a tool the child's own policy resolves to `ask`. A child's own `deny` or `allow` policy for that tool is never overridden by an inherited grant.

**Why this priority**: This is a consent-boundary property — getting it wrong in either direction is a real security or usability regression (silently bypassing a child's own deny, or annoyingly re-prompting for something the parent already approved).

**Independent Test**: Record a grant for the parent, spawn a child with the same tool configured as `ask`, confirm no re-prompt; separately, spawn a child with that tool configured as `deny`, confirm it is still denied despite the inherited grant.

**Acceptance Scenarios**:

1. **Given** a parent agent has "Always Allow"-granted a tool in the current session, **When** it delegates to a child agent whose own policy for that tool is `ask`, **Then** the child's first call to that tool does not prompt again.
2. **Given** the same parent grant, **When** it delegates to a child agent whose own policy for that tool is `deny`, **Then** the child's call to that tool is still denied — the inherited grant has no effect.
3. **Given** the same parent grant, **When** it delegates to a child agent whose own policy for that tool is `allow`, **Then** the child's call proceeds without a prompt — same outcome as an inherited grant would produce, but for a different reason (own policy, not inheritance), and neither the grant store nor the operator's attention is involved.
4. **Given** a parent grants a tool AFTER a child has already been spawned, **When** the child (already running) calls that tool, **Then** the child does NOT retroactively see the new grant — copy-at-spawn semantics, not a live/shared reference.
5. **Given** a grandparent agent holds an "Always Allow" grant for a tool, and delegates to a parent (which inherits it), which in turn delegates to a grandchild, **When** the grandchild — whose own policy for that tool is `ask` — calls that tool, **Then** the call is auto-approved, same as if the grandchild had been delegated to directly by the original grantor.

---

### User Story 3 — The delegation graph's own depth configuration is authoritative, and the prompt truthfully advertises the effective cap (Priority: P1)

Added 2026-07-04 — pulled into this spec's scope from [#477](https://github.com/elicify-ai/omnipus/issues/477), discovered while independently re-verifying MAJ-001 during the 7-reviewer gate fix pass, then corrected in a second pass after operator pushback surfaced the real mechanism (see Clarifications).

Two independent gates check the exact same `ts.depth` counter: the delegation graph's own authorization gate (`enforceEdgeModeAndDepth`, which reads each edge's per-target `Depth` field — the real, operator-facing configuration) and a separate spawn-time backstop (`getSubTurnConfig`/`spawnSubTurn`) that has no knowledge of the graph at all and silently defaults an unset global `SubTurn.MaxDepth` to 3. Two concrete consequences:

1. **The prompt is misleading in the common case.** It mirrors the graph gate's "0 = uncapped" semantics, so an operator who sets nothing sees "max chain depth: uncapped" — but the separate backstop still caps real chains at 3.
2. **The backstop silently overrides explicit operator intent.** An operator who sets a per-edge `Depth: 10` in the delegation graph — the *real*, intended mechanism per this story's title — gets their chain cut off at hop 3 anyway, because the backstop only reads the raw global config field and has no visibility into what the graph already authorized.

**Why this priority**: Raised from the original P2 (cosmetic prompt mismatch) after the second consequence was found — an operator's explicit, deliberate configuration being silently overridden by an unrelated code path is a real functional defect, not just a transparency gap.

**Independent Test**: Configure a delegation-graph edge with `Depth: 10`, leave `SubTurn.MaxDepth` unset; attempt a 5-hop delegation chain through that edge and confirm it succeeds (not silently capped at 3); separately, confirm the delegation prompt's advertised cap for that edge matches what will actually be enforced.

**Acceptance Scenarios**:

1. **Given** an operator has configured a delegation-graph edge with an explicit `Depth: N` (`N > 3`), **When** a delegation chain through that edge reaches a depth between 3 and `N`, **Then** it succeeds — the spawn-time backstop MUST honor the graph's own authorized depth for that edge, not silently re-impose its own default.
2. **Given** an operator has set nothing (`SubTurn.MaxDepth` unset, no per-edge `Depth` on the relevant edge), **When** the delegation system-prompt block is built, **Then** it advertises "max chain depth: 3" — the actual effective cap that will be enforced — not "uncapped."
3. **Given** an operator has explicitly set `SubTurn.MaxDepth` to some value `M > 0` (and no stricter per-edge `Depth` applies), **When** the prompt is built, **Then** it advertises exactly `M`, and a chain of depth `M` is actually enforced at spawn time.
4. **Given** the prompt advertises a specific effective cap for a given delegation path, **When** an agent attempts one hop beyond that advertised cap, **Then** it is rejected with `ErrDepthLimitExceeded` — the advertised number and the enforced number are always the same value, by construction (one shared resolution), not by coincidence.

---

## Behavioral Contract

Primary flows:
- When an agent delegates a task without specifying `async`, the system runs the delegation in the background and returns an immediate acknowledgment.
- When `async: false` is specified, the system blocks until the delegated turn completes and returns its result inline.
- When a spawned child's own tool policy for a tool resolves to `ask`, and the parent already holds an "Always Allow" grant for that tool in the same session, the system auto-approves the child's call without prompting.
- When a delegation-graph edge explicitly authorizes a chain depth deeper than the spawn-time backstop's default, the actual spawn-time enforcement MUST honor that authorized depth — the backstop augments the graph's own authorization, it does not silently override it (User Story 3, added from #477).
- When the delegation system-prompt block advertises a max chain depth, that number MUST be the exact effective value that will actually be enforced for that path — resolved via one shared computation, not two independently-computed readings of the same config field (User Story 3).

Error flows:
- When a spawned child's own tool policy for a tool resolves to `deny`, the system denies the call regardless of any inherited grant.
- When the delegation-policy gate rejects a delegation (trust set, modes, depth), the system surfaces the specific denial reason to the LLM, identically for both sync and async modes.

Boundary conditions:
- When a status check references a `task_id` from a different conversation, the system reports "not found," never another conversation's data.
- When a grant is recorded on the parent after a child has already been spawned, the system does not retroactively apply it to the already-running child.

---

## Edge Cases

- What happens when a child delegates to a grandchild (nested delegation)? **Corrected 2026-07-04, second pass (found during implementation, see Clarifications):** the earlier "no one-level-only guard" claim in this entry was itself wrong. A spawned child's own tool registry excludes the delegation tool entirely (`CloneExcept(ExcludedDelegate, ExcludedHandoff)` — a real, tested, intentional invariant from Sprint H, 2026-04-20, guarded by `TestDelegateCannotSpawnGrandchild`) — a child genuinely CANNOT call `delegate` to reach a grandchild via any real LLM tool call. The `SubTurn.MaxDepth` counter (default 3, #477's fix target) is a separate, additional depth backstop that still matters as defense-in-depth (e.g. if this registry exclusion is ever relaxed to allow deeper chains), but nested delegation chains do NOT actually occur in production today via normal tool use. Grant inheritance's transitivity (FR-D8) is still correctly proven — `Inherit`'s copy-at-spawn semantics are genuinely transitively-inclusive by construction — but the required test exercises this at the `spawnSubTurn` Go-call level directly (bypassing the tool-registry exclusion), not via a real 3-hop LLM delegation chain, since the latter is currently unreachable by design.
- What happens when the parent's grant store has zero grants at the moment of spawning? Expected: `Inherit` is a no-op — the child's own grant set (if any) is untouched, not cleared.
- What happens when the SAME agent ID is spawned as its own child (a worker delegating to a copy of itself)? Expected: `Inherit`'s union semantics mean this is harmless — the "child" already has the exact same grants as the "parent" by identity, and the union is idempotent. Self-delegation increments `depth` identically to delegating to any other agent — there is no exemption (clarified 2026-07-04, MIN-002).
- What happens when `check_spawn_status`'s (or `delegate`'s `action: status`) channel/chatID scoping is bypassed by a direct programmatic call with no channel context? Expected: matches today's documented behavior — "all tasks are listed only when no channel/chat context is injected."
- What happens when an operator revokes a single previously-granted tool (not the whole session) after a child has already been spawned with that grant inherited? Expected (clarified 2026-07-04, MIN-001): there is no single-grant revoke operation today — only whole-session `ClearSession` exists. An already-running child that inherited a grant keeps it until its turn completes or the session is cleared entirely; this asymmetry (new grants don't retroactively apply, but there's no way to retroactively revoke one either) is an accepted limitation of the existing `ApprovalGrantStore`, not something this spec introduces or is scoped to fix.
- Does the existing delegation-policy gate (FR-D3, "FR-6.2") bound concurrent fan-out (breadth), not just depth? Expected (resolved 2026-07-04, MAJ-005 — see Clarifications): yes — `SubTurn.MaxConcurrent` (`pkg/agent/subturn.go`, defaulting to `Performance.EffectiveMaxParallelAgents()` when unset), enforced via a semaphore with a timeout (`ErrConcurrencyTimeout`), already bounds how many concurrent sub-turns a parent can have running. This is an existing mechanism this spec relies on, not a new gap.
- What happens when a delegation-graph edge's own `Depth` field disagrees with the global `SubTurn.MaxDepth` (added 2026-07-04, #477, User Story 3)? Expected: the effective cap is the tighter of the two whenever both are explicitly set (e.g., edge `Depth: 2` + global `7` → effective cap 2; edge `Depth: 10` + global `2` → effective cap 2). When the global config is left unset (0) and the edge expresses no explicit value either (nil = inherit), the effective cap is the safety-backstop default (3). Critically, when the edge DOES express an explicit value and the global config is unset, the edge's value governs — the backstop must not silently reassert its own default of 3 over an operator's explicit per-edge configuration. This is the actual bug FR-D9/FR-D10 fix, corrected from an earlier, incomplete "prompt vs. enforcement" framing (see Clarifications).

---

## Explicit Non-Behaviors

- The system must not let an inherited "Always Allow" grant override a child's own `deny` policy for that tool because that would be a silent, LLM-invisible privilege escalation via delegation — exactly the property User Story 2 exists to prevent.
- The system must not make grant inheritance a live/shared reference between parent and child because a later change to the parent's grants after spawning would then retroactively and unpredictably affect an already-running child — copy-at-spawn is the deliberate, safer semantic.
- The system must not restrict `delegate` to specific agent roles because delegation capability is governed by policy (trust set, modes, depth), not by tool registration — any agent, including the main one, uses the same tool.
- The system must not change `ApprovalGrantStore`'s or `ws_approval.go`'s already-shipped, already-verified logic as part of this spec — this spec adds the missing combined regression test and reconciles the mechanism with the tool merge; it does not re-implement or alter behavior already confirmed correct.

---

## Integration Boundaries

### `pkg/security.ApprovalGrantStore`

- **Data in**: `(sessionID, agentID, tool)` triples for `Record`/`IsAllowed`; `(sessionID, parentAgentID, childAgentID)` for `Inherit`.
- **Data out**: a boolean (`IsAllowed`) or no return value (`Record`/`Inherit`/`ClearSession`).
- **Contract**: nil-receiver-safe (every method fails closed on a nil store or any empty key component) — unchanged, already implemented.
- **On failure**: there is no "failure" mode per se — an ambiguous/empty key always resolves to "ask," never to a silent grant.
- **Development**: real service, in-process, already implemented and tested.

### `pkg/gateway/ws_approval.go` (interactive approval)

- **Data in**: a `ToolApprovalRequest{Tool, SessionID, Meta.AgentID}`.
- **Data out**: an `ApprovalDecision{Verdict}`.
- **Contract**: policy resolves first (allow/deny short-circuit); grant store is only consulted when policy is `ask`. Unchanged by this spec.
- **On failure**: a WS send failure or timeout results in `Deny` (fail-closed), matching existing behavior.
- **Development**: real service in normal operation; the existing test suite (`ws_approval_grants_test.go`) already uses a test double connection (`makeTestConn`) rather than a real browser, which this spec's new tests should also use for consistency.

---

## Verified Behavior (documented, not newly specified)

The following was independently traced through the actual code and cross-checked against existing tests during this session, prior to writing this spec — recorded here so the spec has a durable record of *why* User Story 2 is trusted to already hold, not just asserted:

`wsApprovalHook.ApproveTool` resolves `policy := h.policyResolver(req.Tool, req.Meta.AgentID)` first. A `switch policy { case "allow": return Allow; case "deny": return Deny }` returns immediately for either verdict — the grant store (`h.approvalGrants.IsAllowed(...)`) is only reached when `policy == "ask"`. Since `req.Meta.AgentID` is the identity of whichever agent is *currently* making the call (confirmed via `pkg/agent/subturn.go`'s `newTurnState(&agent, ...)`, which sets a child turn's own `agentID` distinctly from the parent's), a child's own policy is what gets resolved for its own tool calls — an inherited grant is structurally unreachable unless the child's own policy already says `ask` for that exact tool. This is proven directly by the existing `TestApproveTool_PolicyDenyOverridesGrant` and `TestApproveTool_PolicyAllowNeverPrompts` tests (`pkg/gateway/ws_approval_grants_test.go`), though neither literally simulates the parent→child inheritance shape end-to-end — see FR-D5 below for the gap this spec closes.

---

## BDD Scenarios

### Feature: `delegate` — the unified delegation tool

#### Scenario: Default delegation call runs asynchronously

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** an agent with delegation policy permitting an untargeted delegation
- **When** it calls `delegate` with `task: "research X"`
- **Then** the call returns immediately with a `task_id`
- **And** the delegated turn continues running in the background

---

#### Scenario: Explicit sync mode blocks until the result is ready

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** the same agent
- **When** it calls `delegate` with `task: "compute Y"`, `async: false`
- **Then** the call does not return until the delegated turn completes
- **And** the response contains the delegated turn's result inline

---

#### Scenario: Status check on an async delegation reports its real state

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path

- **Given** an async delegation was started via `delegate` and is still running
- **When** the agent calls `delegate` with `action: status`, `task_id: <the handle>`
- **Then** the reported status is `running` — not "no subagents have been spawned"

---

#### Scenario: The main agent delegates using the same tool as any other agent

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the main/orchestrating agent has a delegation policy permitting a target
- **When** it calls `delegate` with that target's `agent_id`
- **Then** the delegation proceeds — access is governed by the policy check, not by any tool-registration restriction based on the caller's role

---

### Feature: Tool-approval grant inheritance on delegation

#### Scenario: Child with tool configured as ask inherits the parent's grant

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a parent agent has previously been granted "Always Allow" for `bash` in the current session
- **And** the parent delegates to a child agent whose own policy for `bash` is `ask`
- **When** the child calls `bash` for the first time
- **Then** the call is auto-approved without an interactive prompt

---

#### Scenario: Child with tool configured as deny is still denied despite an inherited grant

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Error Path

- **Given** the same parent grant for `bash`
- **And** the parent delegates to a child agent whose own policy for `bash` is `deny`
- **When** the child attempts to call `bash`
- **Then** the call is denied
- **But** the denial is due to the child's own policy, not a separate mechanism — the inherited grant is never consulted

---

#### Scenario: Child with tool already configured as allow is unaffected by inheritance either way

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** the same parent grant for `bash`
- **And** the parent delegates to a child agent whose own policy for `bash` is `allow`
- **When** the child calls `bash`
- **Then** the call proceeds without a prompt
- **And** this happens via the child's own policy resolution, not via the grant store

---

#### Scenario: A grant recorded after spawning does not retroactively apply to an already-running child

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a parent has spawned a child with no grants inherited (parent had none at spawn time)
- **And** the parent is later granted "Always Allow" for `bash` while the child is still running
- **When** the already-running child calls `bash` (with its own policy at `ask`)
- **Then** the child is still prompted — the later grant is not visible to it

---

#### Scenario: A grant flows transitively across a three-level delegation chain

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case

- **Given** a grandparent agent has "Always Allow"-granted `bash` in session `S1`
- **And** the grandparent delegates to a parent agent, which inherits the grant
- **And** the parent then delegates to a grandchild agent whose own policy for `bash` is `ask`
- **When** the grandchild calls `bash` for the first time
- **Then** the call is auto-approved without an interactive prompt
- **And** this holds with no new inheritance logic — `Inherit`'s copy-at-spawn semantics already carry it down, since each spawn step copies the immediate parent's *current* bucket, which already includes whatever it inherited upstream

---

#### Scenario: The combined end-to-end path — the regression test this spec exists to add

**Traces to**: User Story 2, Acceptance Scenario 2 (strengthens the existing independent proofs into one chained test)
**Category**: Happy Path

- **Given** a parent agent records an "Always Allow" grant for `bash` in session `S1`
- **And** the parent then delegates to a child agent `child-A`
- **And** `child-A`'s own tool policy for `bash` is `deny`
- **When** `child-A` attempts to call `bash` during its delegated turn
- **Then** the call is denied
- **And** this is verified as one chained integration test — spawn, inherit, resolve policy, deny — not as two independently-true but never-jointly-exercised unit tests

---

### Feature: Delegation-graph depth is authoritative; the prompt matches enforcement

#### Scenario: An explicit per-edge depth deeper than the default backstop is honored

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Error Path (regression guard against the silent-override bug)

- **Given** a delegation-graph edge from agent A to agent B with `Depth: 10`
- **And** `SubTurn.MaxDepth` is left unset globally
- **When** a delegation chain through that edge reaches depth 4 (one past the old default-3 backstop)
- **Then** the delegation succeeds — the spawn-time check honors the edge's authorized depth of 10, not the backstop's own default of 3

---

#### Scenario: Unset depth configuration advertises the true effective default, not "uncapped"

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** an operator has set neither a per-edge `Depth` nor `SubTurn.MaxDepth`
- **When** the delegation system-prompt block is built
- **Then** it advertises "max chain depth: 3"
- **And** this is the same number a 4th delegation hop actually gets rejected at

---

#### Scenario: An explicitly configured global depth is advertised and enforced identically

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** an operator has explicitly set `SubTurn.MaxDepth: 7` and no stricter per-edge `Depth` applies
- **When** the prompt is built
- **Then** it advertises "max chain depth: 7"
- **And** a chain of depth 7 is enforced at spawn time — the 8th hop is rejected

---

#### Scenario: The advertised cap and the enforced cap are computed by one shared function, never two

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Edge Case

- **Given** any combination of per-edge `Depth` and global `SubTurn.MaxDepth` configuration
- **When** both the delegation prompt's advertised cap and the spawn-time enforced cap are computed
- **Then** both values trace to the exact same resolution function/call — proven by a test that intercepts both computations and asserts equality by construction, not by re-implementing the same logic twice and hoping they stay in sync

---

## Test-Driven Development Plan

### Test Hierarchy

| Level       | Scope                        | Purpose                                    |
|-------------|------------------------------|--------------------------------------------|
| Unit        | Tool schema validation, `action` dispatch, delegation-policy gate (unchanged, re-targeted at the merged tool) | Validates the merged tool's own logic |
| Integration | `spawnSubTurn` → grant inheritance → `ApproveTool` policy resolution, chained in one test | Closes the exact gap identified in-session — the combined scenario neither existing test file currently proves end-to-end |
| E2E | Real gateway, a parent granting a tool, delegating, and the child's behavior observed via the real WS approval flow | Validates the user-observable security property |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestDelegate_DefaultIsAsync` | Unit | Scenario: Default delegation call runs asynchronously | Schema default proof |
| 2 | `TestDelegate_AsyncFalseBlocks` | Unit | Scenario: Explicit sync mode blocks until the result is ready | |
| 3 | `TestDelegate_StatusReflectsRealState` | Unit | Scenario: Status check on an async delegation reports its real state | The bug-fix proof — must NOT report "no subagents spawned" |
| 4 | `TestDelegate_MainAgentCanDelegate` | Unit | Scenario: The main agent delegates using the same tool | Confirms no role-based tool restriction exists |
| 5 | `TestApprovalGrant_ChildAskInheritsParentGrant` (existing, re-verify) | Unit | Scenario: Child with tool configured as ask inherits the parent's grant | Already covered by `TestApproveTool_AskWithGrantAutoApproves` — re-run to confirm it still passes post-merge |
| 6 | `TestApprovalGrant_ChildDenyOverridesInheritedGrant` **(NEW)** | Integration | Scenario: Child with tool configured as deny is still denied despite an inherited grant | The specific chained scenario — new |
| 7 | `TestApprovalGrant_ChildAllowUnaffectedByInheritance` **(NEW)** | Integration | Scenario: Child with tool already configured as allow is unaffected by inheritance either way | New, completes the 3-way policy matrix (ask/deny/allow) against inheritance |
| 8 | `TestApprovalGrant_LateGrantNotRetroactive` (existing, re-verify) | Unit | Scenario: A grant recorded after spawning does not retroactively apply | Already covered by `Inherit`'s copy-at-spawn semantics test — re-run to confirm |
| 9 | `TestApprovalGrant_FullChainSpawnInheritDeny` **(NEW, the key addition)** | Integration | Scenario: The combined end-to-end path | Chains `Record` → `spawnSubTurn` (real, not mocked) → `Inherit` → `ApproveTool` in one test, proving the property holds across the real code path, not just two independently-true unit tests |
| 10 | `TestApprovalGrant_TransitiveAcrossThreeLevels` **(NEW)** | Integration | Scenario: A grant flows transitively across a three-level delegation chain | Grandparent → parent → grandchild, real `spawnSubTurn` calls at each hop, proving `Inherit`'s existing copy semantics already carry a grant down more than one level |
| 11 | E2E: real delegation + real WS approval flow | E2E | Scenario: The combined end-to-end path | Full-stack proof |
| 12 | `TestResolveEffectiveDelegationDepth_SharedByPromptAndEnforcement` **(NEW)** | Unit | Scenario: The advertised cap and the enforced cap are computed by one shared function, never two | Core fix for #477 — one function, two call sites (prompt builder, spawn-time check), asserted via the same call, not reimplemented twice |
| 13 | `TestSpawnSubTurn_HonorsExplicitPerEdgeDepthOverDefaultBackstop` **(NEW)** | Integration | Scenario: An explicit per-edge depth deeper than the default backstop is honored | The real bug fix — a depth-10 edge must not be cut off at the old hardcoded default of 3 |
| 14 | `TestWireDelegationInjectors_AdvertisesEffectiveDepthNotRawUncapped` **(NEW)** | Unit | Scenario: Unset depth configuration advertises the true effective default, not "uncapped" | Prompt-side fix |
| 15 | `TestWireDelegationInjectors_AdvertisesExplicitGlobalDepth` **(NEW)** | Unit | Scenario: An explicitly configured global depth is advertised and enforced identically | Confirms explicit `SubTurn.MaxDepth` still works correctly (this case was never broken) |

### Test Datasets

#### Dataset: Child policy × parent grant presence matrix

| # | Parent has grant? | Child's own policy | Expected outcome | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Yes | `ask` | Auto-approved (inherited) | Scenario: Child with tool configured as ask inherits | |
| 2 | Yes | `deny` | Denied | Scenario: Child with tool configured as deny is still denied | The critical row |
| 3 | Yes | `allow` | Approved (own policy, not inheritance) | Scenario: Child with tool already configured as allow | |
| 4 | No | `ask` | Prompted (no grant to inherit) | Regression — existing behavior, unchanged | |
| 5 | No | `deny` | Denied | Regression — trivially unaffected either way | |
| 6 | No | `allow` | Approved | Regression — trivially unaffected either way | |
| 7 | Grandparent only (2 hops away) | `ask` | Auto-approved (inherited transitively) | Scenario: A grant flows transitively across a three-level delegation chain | Proves inheritance composes across `MaxDepth`-permitted chains, not just one hop |

#### Dataset: Effective depth cap resolution (added 2026-07-04, #477)

| # | Per-edge `Depth` | Global `SubTurn.MaxDepth` | Effective cap (advertised AND enforced) | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | nil (inherit) | unset (0) | 3 | Scenario: Unset depth configuration advertises the true effective default | The common case — was "uncapped"/silently-3 mismatch before the fix |
| 2 | nil (inherit) | 7 | 7 | Scenario: An explicitly configured global depth is advertised and enforced identically | Already worked correctly before the fix — regression guard |
| 3 | 10 | unset (0) | 10 | Scenario: An explicit per-edge depth deeper than the default backstop is honored | The real bug — was silently 3 before the fix |
| 4 | 2 | 7 | 2 | (tighter-of, per-edge stricter) | Per-edge cap already correctly won here even before the fix |
| 5 | 10 | 2 | 2 | (tighter-of, global stricter) | Global cap correctly wins when it's the stricter of the two |
| 6 | 0 (edge forbids onward delegation) | (irrelevant) | 0 — no onward delegation at all | (existing behavior, `enforceEdgeModeAndDepth`'s `depth <= 0` guard) | Unaffected by this fix — already correctly denies before reaching depth-cap logic |

### Regression Test Requirements

**If modifying existing functionality:**

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|--------------------|---------------|---------------------------|-------|
| `spawn`/`run_subagent` both call `SpawnSubTurn` with only `Async` differing | Existing `pkg/tools/spawn_test.go`/`subagent_tool_test.go` | Yes — retarget at the merged tool's `Execute`/`ExecuteAsync` | Confirms the merge preserves both modes exactly |
| `ApprovalGrantStore.Inherit` copy-at-spawn semantics | Existing `pkg/security/approvalgrants_test.go::TestApprovalGrantStore_Inherit`, `_InheritNoParentGrant`, `_InheritPreservesChildOwnGrants` | No — these already exist and pass; re-run unmodified as part of this spec's verification | |
| `ApproveTool`'s policy-before-grant ordering | Existing `pkg/gateway/ws_approval_grants_test.go::TestApproveTool_PolicyDenyOverridesGrant`, `_PolicyAllowNeverPrompts`, `_AskWithGrantAutoApproves` | No — these already exist and pass | The GAP is the combined/chained test (Order 9 above), not these individual proofs |
| `check_spawn_status`'s channel/chatID scoping | Existing `pkg/tools/spawn_status_test.go` | Yes — retarget at the merged tool's `action: status`, now reading from the SAME data the merged tool's async path writes to | Fixes the confirmed disconnect as part of the regression suite |

---

## Functional Requirements

- **FR-D1**: The system MUST expose one delegation tool, named `delegate`, with schema `task` (required), `label` (optional), `agent_id` (optional), `async` (optional bool, default **true**), `action` (enum `run|status`, default `run`), `task_id` (required for `action: status`).
- **FR-D2**: The system MUST resolve `action: status` against the exact same underlying state the `async: true` path writes to — no separate, disconnected data structure.
- **FR-D3**: The system MUST apply the delegation-policy gate (trust set, modes, depth — FR-6.2, unchanged) identically regardless of `async` value. Fan-out (breadth) is already bounded by the existing, separate `SubTurn.MaxConcurrent` semaphore (`pkg/agent/subturn.go`, defaulting to `Performance.EffectiveMaxParallelAgents()`) — confirmed 2026-07-04 (7-reviewer gate MAJ-005) as an existing mechanism this spec relies on, not new scope.
- **FR-D4**: The system MUST allow any agent — including the main/orchestrating agent — to use this tool, with access governed entirely by delegation policy, never by tool registration restricted to a role.
- **FR-D5**: The system MUST add an integration test chaining `Record` → real `spawnSubTurn` → `Inherit` → `ApproveTool` policy resolution in one test, proving a child's own `deny` policy overrides an inherited grant across the real code path (not only as two independently-true unit tests).
- **FR-D6**: The system MUST NOT modify `ApprovalGrantStore`'s or `ws_approval.go`'s existing, already-verified logic as part of this change.
- **FR-D7**: Async completion delivery MUST reuse `AsyncNotifier.Notify` (per `async-notifier-spec.md`) unchanged from how `spawn`'s existing callback already does, calling it with `SourceKind: "delegate"` — the corresponding requirement (FR-N11) was added to `async-notifier-spec.md` 2026-07-04 to close a one-directional traceability gap this spec's original FR-D7 left open (MIN-004).
- **FR-D8**: The system MUST prove, via a dedicated integration test spanning a real three-level delegation chain (grandparent → parent → grandchild), that an "Always Allow" grant flows transitively down every hop a delegation chain actually takes (bounded by `SubTurn.MaxDepth`), not just to an immediate child. **Verified 2026-07-04 (7-reviewer gate MAJ-004):** `Inherit`'s real implementation (`pkg/security/approvalgrants.go`, inspected read-only from commit `d0f65482` since it's not yet in the local tree) was read line-by-line, not just re-asserted in prose — confirmed it copies `parentSet` (the parent's *entire current* bucket, whatever it holds at that moment) into the child's bucket via a plain union loop, so a grant the parent itself inherited from *its* parent is included. This is a genuine copy-transitively-inclusive semantic, not per-hop-only — FR-D8's "no new logic required" premise holds.
- **FR-D9** (added 2026-07-04, #477 pulled into scope): The system MUST provide one shared function that resolves the effective delegation-chain depth cap for a given edge — the tighter of (the edge's own `Depth`, when >0) and (`SubTurn.MaxDepth` when explicitly set >0, else the safety-backstop default of `defaultMaxSubTurnDepth`) — and this SAME function MUST be the sole source both the delegation system-prompt builder (`wireDelegationInjectors`) and the spawn-time enforcement check (`spawnSubTurn`) consult. No second, independently-maintained computation of this value may exist anywhere in the codebase.
- **FR-D10** (added 2026-07-04, #477 pulled into scope — the core bug fix): `spawnSubTurn`'s depth check MUST honor an edge's own explicit `Depth` when it is stricter *or* more permissive than the safety-backstop default — an operator's explicit per-edge configuration in the delegation graph MUST NOT be silently overridden by the spawn-time backstop. The backstop (`defaultMaxSubTurnDepth`) applies only when neither the edge nor the global config expresses an explicit value.

---

## Success Criteria

- **SC-001**: Checking the status of a task spawned via `delegate`'s async mode reports the task's real, current state 100% of the time — zero occurrences of the "no subagents have been spawned yet" false-negative.
- **SC-002**: The child-deny-overrides-inherited-grant property is proven by a real, chained integration test (not just two independently-true unit tests), and that test passes.
- **SC-003**: A main/orchestrating agent successfully delegates using `delegate` in at least one E2E test, with no tool-registration-based restriction encountered.
- **SC-004**: A grant held by a grandparent is visible to a grandchild two delegation hops away, proven by a real (not mocked) three-level integration test.
- **SC-005** (added 2026-07-04, #477): A delegation-graph edge configured with `Depth: 10` permits a real, actual delegation chain to reach depth 10 — not silently capped at 3 — proven by a real (not mocked) integration test spawning through that edge.
- **SC-006** (added 2026-07-04, #477): For every row in the "Effective depth cap resolution" dataset, the delegation prompt's advertised cap and the spawn-time enforced cap are identical, verified by a test that calls the same shared resolution function for both and asserts they were never computed independently.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|------------------|---------------|
| FR-D1 | US-1 | Scenario: Default delegation call runs asynchronously; Scenario: Explicit sync mode blocks | `TestDelegate_DefaultIsAsync`, `TestDelegate_AsyncFalseBlocks` |
| FR-D2 | US-1 | Scenario: Status check on an async delegation reports its real state | `TestDelegate_StatusReflectsRealState` |
| FR-D3 | US-1 | (delegation-policy gate, unchanged, covered by existing FR-6.2 tests re-targeted) | Existing delegation-policy tests, retargeted |
| FR-D4 | US-1 | Scenario: The main agent delegates using the same tool | `TestDelegate_MainAgentCanDelegate` |
| FR-D5 | US-2 | Scenario: The combined end-to-end path | `TestApprovalGrant_FullChainSpawnInheritDeny` |
| FR-D6 | Explicit Non-Behaviors | Scenario: Child with tool configured as deny is still denied; Scenario: Child with tool already configured as allow | `TestApprovalGrant_ChildDenyOverridesInheritedGrant`, `TestApprovalGrant_ChildAllowUnaffectedByInheritance` |
| FR-D7 | (shared with `async-notifier-spec.md`) | See that spec | (defined in `async-notifier-spec.md`) |
| FR-D8 | US-2 | Scenario: A grant flows transitively across a three-level delegation chain | `TestApprovalGrant_TransitiveAcrossThreeLevels` |
| FR-D9 | US-3 | Scenario: The advertised cap and the enforced cap are computed by one shared function, never two | `TestResolveEffectiveDelegationDepth_SharedByPromptAndEnforcement`, `TestWireDelegationInjectors_AdvertisesEffectiveDepthNotRawUncapped`, `TestWireDelegationInjectors_AdvertisesExplicitGlobalDepth` |
| FR-D10 | US-3 | Scenario: An explicit per-edge depth deeper than the default backstop is honored | `TestSpawnSubTurn_HonorsExplicitPerEdgeDepthOverDefaultBackstop` |

**Completeness check**: every FR-xxx has at least one BDD scenario and test; every BDD scenario appears above.

---

## Ambiguity Warnings

All three ambiguities identified during drafting were resolved by the operator on 2026-07-04 — see Clarifications below. None remain open.

---

## Evaluation Scenarios (Holdout)

> For post-implementation evaluation only. Not referenced in the TDD plan or traceability matrix.

### Scenario: An operator grants a tool once, then delegates several times in a row
- **Setup**: Real gateway, a parent agent, an operator interactively approves "Always Allow" for one tool.
- **Action**: The parent delegates to three different children in the same session, each with that tool configured as `ask`.
- **Expected outcome**: None of the three children re-prompt for that tool.
- **Category**: Happy Path

### Scenario: A chain of specialized workers with mixed policies, several hops deep
- **Setup** (sharpened 2026-07-04, MIN-003 — the original version of this scenario duplicated Acceptance Scenario 2 exactly and added no new coverage): Main agent has "Always Allow" for `bash`; it delegates to worker A (`bash: ask`, inherits and auto-approves), which delegates to worker B (`bash: deny`), which — if it could — would delegate to worker C.
- **Action**: Observe each hop's behavior for the same tool as policies vary across the chain.
- **Expected outcome**: Worker A runs `bash` without a prompt (inherited grant, its own policy is `ask`); worker B cannot run `bash` under any circumstance (its own `deny` overrides the inherited grant it also received); the chain's behavior at each hop is governed independently by that hop's own policy, never by what an ancestor further up the chain was granted.
- **Category**: Error

### Scenario: Checking on a subagent's status from a different conversation
- **Setup**: A subagent is spawned from conversation A; a completely unrelated conversation B attempts to check its status by guessing/reusing the task_id.
- **Action**: Conversation B calls `delegate` with `action: status` and that `task_id`.
- **Expected outcome**: "Not found" — conversation B never sees conversation A's task data.
- **Category**: Error

### Scenario: The main agent uses `delegate` exactly like a specialized agent would
- **Setup**: Real gateway, the main/default agent (e.g., Mia) and a specialist worker (e.g., a research agent) both configured with equivalent delegation policy.
- **Action**: Both delegate an equivalent task using `delegate`.
- **Expected outcome**: Behavior is identical between them — nothing about being "the main agent" changes how the tool works.
- **Category**: Happy Path

### Scenario: A very long-running async delegation, checked on repeatedly over several minutes
- **Setup**: A delegation that takes several minutes to complete.
- **Action**: Poll its status every 30 seconds.
- **Expected outcome**: Status transitions correctly from `running` to `completed` (or `failed`) exactly once, at the right time, never flickering or reverting.
- **Category**: Edge Case

---

## Assumptions

- `d0f65482`'s `ApprovalGrantStore`/`ws_approval.go` mechanism is correct as shipped and is not modified by this spec — only reconciled with the tool merge and given the one missing chained regression test (FR-D5).
- `delegate` is the final, locked name — used literally throughout this spec's schema, test names, and traceability matrix.
- Retiring `SubagentManager` is in scope for this change (see Clarifications) — the implementation should delete it, not just route around it.
- Fan-out (breadth) is already bounded by the existing `SubTurn.MaxConcurrent` semaphore — confirmed 2026-07-04, not a gap needing new scope (MAJ-005).
- There is no single-grant revoke operation today, only whole-session `ClearSession` — this asymmetry (new grants aren't retroactive; neither are revocations) is an accepted, pre-existing limitation this spec doesn't fix (MIN-001).
- The delegation graph's per-edge `Depth` field is the real, operator-facing depth-configuration mechanism; `SubTurn.MaxDepth`'s `defaultMaxSubTurnDepth` (3) is a separate safety backstop that applies only when neither the edge nor the global config expresses an explicit value — the two must compose (tighter-of), not silently override one another (#477, User Story 3).

## Clarifications

### 2026-07-04

- Q: Does a subagent inherit tool approval from its parent unconditionally, or only when it would otherwise have to ask? -> A: Only when the child's own policy for that tool resolves to `ask` — verified in-session by tracing `ws_approval.go`'s policy-before-grant ordering, and formalized here as User Story 2 with the one missing combined regression test (FR-D5) identified and specified.
- Q: What should the unified delegation tool be named? -> A: `delegate` — plain, action-oriented, no collision with existing tool names, matches how the feature is already described elsewhere in project docs.
- Q: Should retiring the legacy `SubagentManager` type be in scope for this change? -> A: Yes — it's already marked legacy in its own code comment, and merging the three tools is the natural moment to remove it rather than leaving a second, now-pointless internal code path behind the new tool.
- Q: Should nested delegation (grandparent → parent → child) inherit grants transitively? -> A: Yes — in scope, and the underlying `Inherit` mechanism genuinely supports it (copy-at-spawn semantics are transitively-inclusive by construction, no new inheritance code needed). **Second correction, found during implementation (2026-07-04):** an earlier revision of this entry claimed "there is no one-level-only delegation guard" — that claim was itself wrong. A spawned child's tool registry excludes `delegate` entirely (`ExcludedDelegate`/`ExcludedHandoff`, a real, tested, intentional Sprint-H invariant, `TestDelegateCannotSpawnGrandchild`), so a real 3-hop LLM-driven delegation chain cannot occur today. FR-D8's required test proves transitivity correctly at the `spawnSubTurn` Go-call level (bypassing the registry exclusion to exercise `Inherit` directly), which is the right scope given the exclusion is a separate, unrelated invariant this spec does not change.

### 2026-07-04 — 7-reviewer gate fixes (post-grill)

- Q [MAJ-001]: Is `SubTurn.MaxDepth`'s real default actually 3, or is it 0/uncapped as the review claimed (citing `pkg/agent/delegation_context.go:41`'s "0 = uncapped" comment)? -> A: **The review's claim was checked and found WRONG for the spawn-time enforcement path.** `pkg/agent/subturn.go`'s `getSubTurnConfig()` — the function that gates spawning via `parentTS.depth >= rtCfg.maxDepth` — explicitly does `if maxDepth <= 0 { maxDepth = defaultMaxSubTurnDepth /* = 3 */ }`. This spec's original "default 3" claim was correct for that path.
- Q [#477, follow-up correction after operator pushback]: Is the bug really just "the prompt says uncapped but enforcement defaults to 3"? -> A: **No — that framing was itself incomplete, corrected 2026-07-04.** There are two independent gates on the same `ts.depth` counter: the delegation graph's own authorization gate (`enforceEdgeModeAndDepth`, which reads each edge's own `Depth` field — the real, operator-facing configuration mechanism — plus the raw `SubTurn.MaxDepth`, treating 0 as "no constraint"), and the separate spawn-time backstop (`getSubTurnConfig`) that treats the same raw 0 as "default to 3" and has NO knowledge of the graph's per-edge `Depth` at all. The prompt (mirroring the graph gate) isn't lying about that gate — but an operator who explicitly configures a per-edge `Depth: 10` gets silently cut off at hop 3 by the unrelated backstop anyway. This is a real functional defect (an operator's explicit configuration silently overridden), not just a display bug — now fixed by FR-D9 (one shared effective-cap resolution) and FR-D10 (the backstop must honor an explicit per-edge depth), User Story 3, pulled fully into this spec's scope rather than left as a standalone #477 follow-up.
- Q [MAJ-004]: Is FR-D8's "no new inheritance logic required" claim actually verified against `Inherit`'s real implementation, or only asserted in prose? -> A: Verified for real. `d0f65482`'s `pkg/security/approvalgrants.go` (inspected read-only via `git show d0f65482:pkg/security/approvalgrants.go`, since the commit isn't in the local tree yet) shows `Inherit` copies `parentSet` — the parent's entire current grant bucket — into the child's bucket via a plain union loop. Since `parentSet` already includes anything the parent itself inherited from an earlier `Inherit` call, this is genuinely transitively-inclusive by construction. FR-D8's premise holds.
- Q [MAJ-005]: Does anything bound delegation fan-out (breadth), or only depth? -> A: Fan-out is already bounded — `SubTurn.MaxConcurrent` (defaulting to `Performance.EffectiveMaxParallelAgents()` when unset), enforced via a semaphore with `ErrConcurrencyTimeout`. Not a gap; cited explicitly in FR-D3 now.
- Q [MAJ-003, shared with `tool-consolidation-spec.md`]: Does the companion overview doc still say "subagent" with "name TBD"? -> A: Fixed — `tool-consolidation-spec.md` now states the resolved name (`delegate`) throughout and points to this spec's Clarifications as the resolution record.
- Q [MIN-002]: Does self-delegation (an agent delegating to a copy of itself) get any exemption from depth accounting? -> A: No — self-delegation increments `depth` identically to any other delegation hop; no special case.
