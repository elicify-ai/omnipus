# Spec — Browser: one shared Chrome + per-agent browser contexts (ADR-043)

- **Source ADR:** `docs/internal/architecture/ADR-043-browser-shared-chrome-per-agent-contexts.md` (grilled once; findings folded in)
- **Branch:** `bugfixes2` · **Repo:** `/home/dev/omnipus3`
- **Status:** Draft for two grill-spec rounds → implementation
- **Operator profile (confirmed):** up to ~10 browser-using agents, browsing **often simultaneous**; **cookie/login isolation per agent sufficient** (no process isolation required for v1); single-Chrome crash blast radius **accepted**.

## 1. Overview / Actors / Scope

**Problem.** Every per-agent `BrowserManager` launches its own Chrome on the fixed CDP port 9223 (`pkg/tools/browser/manager.go:40,564`). Only the first browser-using agent can launch; every other agent's first browser call fails (today cleanly via `checkDebugPortAvailable`, `manager.go:81-83`). On the seeded policy only Jim + Ray browse (`pkg/coreagent/core.go`), so 2 of 4 collide; custom browser-using agents collide too.

**Solution (ADR-043).** One gateway-scoped Chrome owned by a `BrowserCoordinator`; each browser-using agent connects to it and gets its own **CDP browser context** (isolated cookies/localStorage + its own ADR-041 tab-set). Browser contexts give per-agent cookie/login isolation **within one Chrome process** — the trade-off the operator provisionally accepted is mitigated by `chromedp.WithNewBrowserContext()` (verified, chromedp 0.15.1).

**Actors:** `BrowserCoordinator` (new, owns the Chrome process); `BrowserManager` (per-agent, owns connection + its browser context); the gateway WS handler (`pkg/gateway/browser_ws.go`); the live-view engine (`pkg/tools/browser/live.go`); the SPA `BrowserLivePanel`.

**In scope (v1):**
- `BrowserCoordinator` (D1 + D4 ownership contract) — single launch authority, ownership marker, refcount, launcher-wait crash detector, gateway-`Close()` kill.
- Per-agent browser contexts (D2) — `WithNewBrowserContext`, tab-set/screencast bound to a context's active tab.
- Live-view per-agent binding (D3) — no wire change; description-prose update; multi-agent take-the-wheel UX.
- Global tab budget (D7) — `tools.browser.max_total_tabs` (default 30), coordinator-enforced.

**Out of scope (deferred escape hatch, ADR-043):** browser-process pool (1→M Chromes); per-agent hard-isolation opt-in (own Chrome process); `Session(ctx)` cancellation threading; `omnipus browser install` CLI; the `tools.browser.preprovision` opt-out (#507).

## 2. Existing Codebase Context

### Symbols involved
| Symbol | Role | Context |
|---|---|---|
| `AgentLoop.browserMgrs` (`loop.go:185`) | **modifies** | map[agentID]→`*BrowserManager`; one per agent today |
| `registerSharedTools` (`loop.go:~1311-1717`) | **modifies** | per-agent manager creation + the `prior.Shutdown()` reload path (`loop.go:1712-1713`) → becomes `coordinator.Release(agentID)` |
| `BrowserManager.ensureStarted` (`manager.go:~434-599`) | **modifies** | managed-mode branch builds its own ExecAllocator (`:564`) → asks coordinator instead |
| `BrowserManager.Shutdown` (`manager.go:2070`) | **modifies** | today cancels the ExecAllocator (kills Chrome) → drops connection+context only |
| `BrowserManager.bootstrapBrowserCtx` (`manager.go:~1094`) | **modifies** | gains `WithNewBrowserContext()`; tabs inherit the context |
| `chromedp.NewRemoteAllocator` path (`manager.go` CDPURL branch) | **reuses** | unchanged; coordinator populates it automatically |
| gateway WS handler (`browser_ws.go:530,544`) | **no change** | already binds on `agent_id` → `BrowserManagerForAgent` → `Live().Attach(DefaultSessionID)` |
| `sandbox_apply.go:388,419` | **no change** | one port 9223 already allow-listed |
| `BrowserAttachFrame.yaml` | **modifies (prose only)** | description wording |
| `tools.browser.max_total_tabs` | **adds** | new config field (Stream D) |

### Impact assessment
| Symbol modified | Risk | Direct dependents (d=1) | Indirect (d=2) |
|---|---|---|---|
| `BrowserManager.Shutdown` semantics | **CRITICAL** | `registerSharedTools` reload (`loop.go:1712`), `AgentLoop.Close` (`loop.go:2792`) | every browser tool, live-view, hot-reload |
| `ensureStarted` (launch→connect) | **HIGH** | every browser tool via `Session()` | live-view attach |
| `registerSharedTools` (Release) | **HIGH** | `ReloadProviderAndConfig`, `NewAgentLoop` | all agents on reload |
| `bootstrapBrowserCtx` (+context) | **HIGH** | tab create/adopt, screencast | ADR-041 tab tools |
| `BrowserAttachFrame.yaml` prose | LOW | SPA consumers | none (no validation change) |

## 3. Implementation Streams (fan-out for parallel agents)

Five streams with **explicit interfaces** so dev agents run concurrently without collision. Stream A is the critical path; B–D depend on A's interface but not its internals.

### Shared interface contract (all streams code against this — define FIRST, in Stream A's first commit)
```go
// pkg/tools/browser/coordinator.go (new) — placement RESOLVED (round-2 MIN-007):
// same package as BrowserManager (avoids import cycle; owns Chrome + contexts).
type BrowserCoordinator struct { /* ... */ }

// Register associates a manager with the coordinator and returns the cdpURL to
// dial + the agent's STABLE browser-context id. THE COORDINATOR CREATES+OWNS the
// context (WithNewBrowserContext on the coordinator's own long-lived chromedp
// context, which survives reload because ReloadProviderAndConfig reuses the same
// *AgentLoop). The manager RE-ADOPTS it non-owningly via WithExistingBrowserContext(id)
// (chromedp.go:511-518 — non-owning, no auto-dispose). This is the CRIT-003 fix:
// a WithNewBrowserContext on a MANAGER path would auto-dispose on the manager's
// reload-time cancel, destroying cookies every Settings save.
// INVARIANT: no WithNewBrowserContext call exists on any *BrowserManager path.
// Launches Chrome if none live (without holding the coordinator mutex across the
// blocking launch — ADR-038 discipline, round-2 MAJ-007). Errors if launch fails.
func (c *BrowserCoordinator) Register(ctx context.Context, agentID string, mgr *BrowserManager) (cdpURL string, browserCtxID cdp.BrowserContextID, err error)

// Release drops an agent's manager ref on reload. Does NOT kill Chrome and does NOT
// dispose the agent's browser context (the coordinator owns it; it persists keyed
// by agentID so the next Register re-adopts it — cookies/localStorage/login survive
// a Settings save). Open TABS may be lost on reload (targets are session-owned;
// agents reopen) — round-2 MAJ-006. Disposal happens only on agent-removal/Close().
func (c *BrowserCoordinator) Release(agentID string) int

// TryOpenTab atomically checks the global tab budget and, if under it, reserves a
// slot — under ONE coordinator lock — so concurrent openers at the boundary see
// exactly one winner (round-2 MAJ-007; the §10 budget-race dataset). Returns
// (allowed bool, reason string).
func (c *BrowserCoordinator) TryOpenTab(agentID string) (bool, string)

// TotalOpenTabs sums open tabs across registered managers' OpenTabCount() (round-1 MAJ-001).
func (c *BrowserCoordinator) TotalOpenTabs() int
// PID returns the shared Chrome pid (0 if none) — R1/R3 tests (round-1 MAJ-005).
func (c *BrowserCoordinator) PID() int
// Shutdown disposes all contexts + kills the Chrome process — called ONLY by gateway Close().
func (c *BrowserCoordinator) Shutdown()
```
**Coordinator locking discipline (round-2 MAJ-007):** `Register`/`Release`/`TryOpenTab`/`TotalOpenTabs`/`PID` take a coordinator mutex for bookkeeping; `Register` RELEASES it around the blocking Chrome launch+context-create (never holds it across a CDP/exec call — ADR-038 no-lock-across-blocking-call). `TryOpenTab` is the one atomic check-and-reserve so the budget boundary is race-free. The `BrowserManager` gains: a `coordinator *BrowserCoordinator` field; an exported `OpenTabCount() int`; `ensureStarted` calls `coordinator.Register` → builds `NewRemoteAllocator(cdpURL)` → re-adopts `browserCtxID` via `WithExistingBrowserContext`. `Close()` (`loop.go:2785-2798`) adds a single `coordinator.Shutdown()` as the SOLE process-kill path (round-2 MIN-008); per-manager `Shutdown()` drops only the manager's connection.

### Stream A — Coordinator (D1 + D4) [CRITICAL PATH]
**Owns:** `pkg/tools/browser/coordinator.go` (new), the `BrowserManager.coordinator` field + `ensureStarted`/`Shutdown` rewrites, `registerSharedTools` Release change, the `loop.go:1712` reload path, gateway `Close()` wiring.
**Depends on:** nothing (foundational). **Interface out:** the contract above.
**Invariants (grill C1 — load-bearing):** (1) `allocCtx`/`allocCancel` live on the coordinator; (2) `manager.Shutdown()` closes the manager's DialCtx **and, on the RELOAD path, does NOT dispose the browser context** — the context persists keyed by `agentID` (CRIT-002) so the new manager re-adopts it (cookies/login/tabs survive a Settings save); context disposal happens only on agent-removal or gateway `Close()`; (3) `prior.Shutdown()` → `coordinator.Release(agentID)` (refcount, no process kill, no context dispose); (4) process kill only on gateway `Close()`.
**Crash detection (grill M1):** launcher manager `os/exec` `Wait()`-based detector → coordinator relaunch → unblock connectors; connectors reset `m.started` on connection drop and re-ask the coordinator. **Crash recovery is into FRESH contexts** (CRIT-001): CDP browser contexts are in-memory and do NOT survive a Chrome process restart, so a crash loses per-agent cookies/login by definition — the recovery guarantee is "all agents browsing again within T in fresh empty contexts," NOT "cookies intact" (cookie snapshot/restore via `Network.getCookies`/`setCookie` is a documented future enhancement, out of v1 scope).
**Ownership marker (grill M2):** `$OMNIPUS_HOME/browser/shared-chrome.pid` (pid + chrome-for-testing version); a cold start verifies a holder is *our* Chrome before adopting; foreign holders are rejected (no probe-and-dial).

### Stream B — Per-agent browser contexts (D2) [SPIKE-GATED]
**Owns:** tab creation/inheritance of `BrowserContextID` within the **coordinator-owned** context; `adoptTarget` on a child context; screencast re-bind to a context's active tab; the manager's non-owning re-adoption of its context via `WithExistingBrowserContext(browserCtxID)`.
**Depends on:** Stream A's coordinator (which creates+owns each agent's context and returns its id via `Register`).
**TDD test #1 (the spike — gates the whole stream):** *a `window.open` from agent A's tab creates the new target in agent A's browser context, invisible to agent B* (the O1 property; CDP defaults new targets to the opener's context — verify, don't assume).
**CRIT-003 invariant (load-bearing):** the manager NEVER calls `WithNewBrowserContext` — it only re-adopts the coordinator-owned context non-owningly (`WithExistingBrowserContext`). A `WithNewBrowserContext` on the manager path would auto-dispose on the manager's reload-time cancel, destroying cookies every Settings save.
**Spike fallback (grill MAJ-003):** if the O1 property does NOT hold, this is a stop-work decision point — escalate to re-ADR (fallback: per-agent Chrome **profiles** within one Chrome instead of contexts, or revert to serialize-until-fixed). Do NOT silently ship cross-agent cookie bleed.
**Interface out:** a manager's tab-set is scoped to its re-adopted context; `Live().Attach` binds to the active tab within that context; `OpenTabCount()` exposes the per-agent count for Stream D.

### Stream C — Live-view per-agent binding + contract (D3)
**Owns:** confirm `browser_ws.go:530,544` already disambiguates by `agent_id` (no code change expected); `BrowserAttachFrame.yaml` description prose update; SPA `BrowserLivePanel` multi-agent UX (show *which agent's* context is driven — the header/chip).
**Depends on:** Stream B (contexts exist). **No wire-schema change** (D3 verified). `make verify-contracts` must stay green.

### Stream D — Global tab budget (D7)
**Owns:** `BrowserToolConfig.MaxTotalTabs` (`pkg/config/config.go`, json `max_total_tabs`, default 30, env doc-only); `OpenTabTool` enforcement — before opening, call `coordinator.TotalOpenTabs()` (which sums the registered managers' `OpenTabCount()`, grill MAJ-001) and deny with a clear error when `≥ max_total_tabs` (agent can `browser_close_tab` first); the per-agent `MaxTabs` stays as a courtesy cap.
**Depends on:** Stream A's `Register`/`TotalOpenTabs` interface + Stream B's per-context `OpenTabCount()` (Stream D codes against the interface, so it can start in parallel with B once the interface is committed — MAJ-001 resolved).

### Stream E — Tests + contracts (cross-cutting)
**Owns:** the R1–R3 regression tests (below), the 5-agent concurrent stress harness, `make verify-contracts`, the contract-description prose review. Runs alongside every stream as its TDD partner.

**Parallelization note:** A is the critical path and lands first (its interface). Once the interface contract is committed, B/C/D fan out in parallel (different files: `coordinator.go` vs `manager.go` bootstrap vs `browser_ws.go`/SPA vs `config.go`/`tools.go`). E is continuous.

## 4. Behavioral contract (observable)
- When ≥2 browser-using agents call browser tools concurrently, each gets its own isolated browsing session; none fails with "port in use".
- When an agent logs into a site in its context, no other agent is logged in as it.
- When the operator saves any Settings while agents are browsing, **login/localStorage survive** — the shared Chrome process is not killed AND each agent's browser context is **coordinator-owned** (survives the manager swap; re-adopted by the new manager via `WithExistingBrowserContext`). Open **tabs may be lost** on reload (targets are session-owned; agents reopen them) — round-2 MAJ-006. (grill CRIT-002 + CRIT-003.)
- When the shared Chrome **crashes** mid-session, all agents recover into **fresh empty contexts** within a bounded T (CDP contexts are in-memory and do not survive a process restart — per-agent cookies/login are lost on a crash by definition; grill CRIT-001). This is the accepted single-Chrome blast radius.
- When the total open tabs across all agents would exceed `max_total_tabs`, `browser_open_tab` is denied with an actionable error.
- When the gateway stops, the Chrome process is killed exactly once.

## 5. Explicit non-behaviors
- The system must **not** kill the shared Chrome on per-agent `Shutdown()`/reload (grill C1) — only on gateway `Close()`.
- The system must **not** probe-and-dial port 9223 (grill M2) — managers ask the coordinator only.
- The system must **not** share cookies/localStorage/login across agents (D2 isolates them).
- The system must **not** add a wire-schema field change for `browser_attach` (D3 — `agent_id` already binds).
- The system must **not** run >1 managed Chrome in v1 (no pool — deferred).
- The system must **not** hold the manager mutex across a CDP launch/connect call (ADR-038 discipline).

## 6. Integration boundaries
- **chromedp / CDP** (`Target.createBrowserContext`, `disposeBrowserContext`, remote allocator): in-process; failure → manager errors the tool call clearly; the D2 spike confirms the `window.open`-to-opener-context property.
- **Sandbox (Landlock/seccomp)**: unchanged — one port 9223 on the fixed allow-list. No new ports.
- **SPA live-view WS**: no schema change; the panel gains an agent-identity chip. Failure (WS drop) → re-attach to the same agent's context.
- **Config**: new `tools.browser.max_total_tabs` (default 30); existing `cdp_url` still forces a fully-external Chrome (coordinator bypassed).

## 7. User stories & acceptance criteria (selection — full set in §9 traceability)

**US-1 (P0) Concurrent browsing.** As an operator running multiple browser-using agents, I want all of them to browse at once so work isn't serialized.
- *Why P0:* the core defect; the reason for the ADR.
- *Independent test:* 2+ agents each `browser_navigate` a distinct URL concurrently → both succeed, each renders its own page.
- AC1: **Given** agents Jim and Ray both have browser policy, **When** both call `browser_navigate` concurrently, **Then** both succeed and neither sees "port 9223 in use".

**US-2 (P0) Per-agent cookie isolation.** As an operator, I want agents isolated so one agent's login never leaks to another.
- AC1: **Given** agent A logs into a site in its context, **When** agent B opens the same site, **Then** B is not authenticated as A.

**US-3 (P0) Hot-reload safety (grill C1 + CRIT-002 + CRIT-003).** As an operator, saving Settings while agents browse must not drop **login/localStorage**.
- AC1: **Given** two agents are browsing with a site login each, **When** `ReloadProviderAndConfig` runs (any Settings save), **Then** the shared Chrome PID is unchanged AND each agent's **coordinator-owned** context is re-adopted (the login/localStorage persists); open tabs may be lost (agents reopen).

**US-4 (P0) Crash recovery (grill M1 + CRIT-001).** As an operator, a Chrome crash must self-heal within a bound — into fresh contexts (cookies lost by definition).
- AC1: **Given** agents are browsing, **When** the Chrome process is killed, **Then** all agents recover within T (≤10 s) into fresh empty contexts (browsing works again; per-agent logins are NOT preserved — documented limitation).

**US-5 (P1) Global tab budget (D7).** As an operator on a sized host, I want a hard cap on total tabs so concurrent browsing can't OOM.
- AC1: **Given** `max_total_tabs=30` and 30 tabs open across agents, **When** any agent calls `browser_open_tab`, **Then** it is denied with an error naming the budget and suggesting `browser_close_tab`.

**US-6 (P1) Take-the-wheel multi-agent clarity (D3/ADR-040).** As a human driving an agent's browser, I must know which agent I'm driving.
- AC1: **Given** multiple agents are browsing, **When** I open the live panel for agent Jim, **Then** the panel shows Jim's context only and labels it as Jim's.

**US-7 (P1) No wire change (D3).** As a maintainer, the contract must not break.
- AC1: **Given** the implementation, **When** `make verify-contracts` runs, **Then** it is green (only `BrowserAttachFrame.yaml` description prose changed).

**US-8 (P1) No foreign-Chrome adoption (grill M2 / FR-007).** As an operator, the gateway must never silently drive an unrelated Chrome already on 9223.
- AC1: **Given** an unrelated Chrome (wrong version / no marker) holds 9223 at start, **When** the coordinator initializes, **Then** it rejects (not adopts) the foreign Chrome and surfaces a clear error.

**US-9 (P1) Gateway stop is the only kill (FR-008).** As an operator, the Chrome process must die exactly once, only on gateway stop.
- AC1: **Given** agents are browsing, **When** reloads + per-agent `Release` happen, **Then** `coordinator` killCount stays 0; **and when** `Close()` runs, **then** killCount becomes 1 and the pid is dead.

**US-10 (P1) Live-view binds per agent (FR-010 / D3).** As a human, watching agent N shows N's context.
- AC1: **Given** multiple agents browsing, **When** the WS handler resolves `BrowserManagerForAgent(N)`, **Then** the screencast binds to N's context's active tab only.

## 8. BDD scenarios (key — full traceability in §9)

**Scenario: two agents browse concurrently (Happy Path) — Traces to US-1/AC1, FR-001**
- **Given** a shared Chrome is running via the coordinator and agents Jim and Ray each have a browser context
- **When** Jim navigates to `https://example.com/a` and Ray to `https://example.com/b` concurrently
- **Then** both navigations succeed and Jim's page shows "a", Ray's shows "b"

**Scenario: window.open stays in opener's context (Edge Case / D2 spike — the gating property) — Traces to US-2, FR-002**
- **Given** agent A's tab is on a page with a `target="_blank"` link and agent B has its own context
- **When** A clicks the link (opens a new tab)
- **Then** the new target's `browserContextId` equals A's context id AND B's `browser_list_tabs` does not include it

**Scenario: hot-reload preserves login/localStorage (grill C1 + CRIT-002 + CRIT-003) — Traces to US-3, FR-004**
- **Given** the shared Chrome PID is `coordinator.PID()==P` and two agents are browsing with a site login each (coordinator-owned contexts)
- **When** `ReloadProviderAndConfig` runs
- **Then** `coordinator.PID()==P` still (process not killed) AND each agent's context is re-adopted by the new manager (the **login/localStorage persists**) AND no `WithNewBrowserContext` was called on any manager path

**Scenario: Chrome crash recovers into fresh contexts within bound (grill M1 + CRIT-001) — Traces to US-4, FR-005**
- **Given** agents are browsing
- **When** the Chrome process is killed (`coordinator.PID()` dies)
- **Then** within 10 s `coordinator.PID()` is a new live pid AND all agents' next `browser_navigate` succeeds in **fresh empty contexts** (prior logins are NOT present — documented limitation)

**Scenario: tab budget denial (D7) — Traces to US-5, FR-006**
- **Given** `max_total_tabs=2` and 2 tabs open across agents
- **When** any agent calls `browser_open_tab`
- **Then** the tool returns an error containing "max_total_tabs" and "browser_close_tab"

**Scenario: foreign Chrome on 9223 is not adopted (grill M2) — Traces to FR-007**
- **Given** an unrelated Chrome (wrong version / no marker) holds 9223
- **When** the coordinator starts
- **Then** it does NOT attach to the foreign Chrome AND surfaces a clear error (or launches a distinct managed instance per the marker check) rather than silently driving the wrong browser

**Scenario: no wire-schema change holds (D3) — Traces to US-7, FR-009**
- **Given** the implementation is complete
- **When** `make verify-contracts` runs
- **Then** it exits 0 with only `BrowserAttachFrame.yaml` description prose changed (no field/enum/maxLength delta)

**Scenario: gateway stop is the sole Chrome kill (FR-008) — Traces to US-9, FR-008**
- **Given** agents are browsing and `coordinator.killCount==0`
- **When** a reload + per-agent `Release` occur
- **Then** `killCount` is still 0 and `coordinator.PID()` is unchanged
- **And when** `AgentLoop.Close()` runs
- **Then** `killCount==1` and the pid is no longer in `/proc`

**Scenario: live-view binds to the attached agent's context (FR-010) — Traces to US-10, FR-010**
- **Given** agents Jim and Ray are browsing in distinct contexts
- **When** a viewer attaches with `agent_id=Ray`
- **Then** the screencast renders Ray's context's active tab and NOT Jim's

## 9. Traceability matrix (FR ↔ US ↔ BDD ↔ test ↔ ADR/grill)

| FR | US | BDD | Test (TDD) | ADR / grill |
|---|---|---|---|---|
| FR-001 concurrent browsing works | US-1 | two-agents-concurrent | `TestCoordinator_TwoAgentsConcurrent` | D1/D4 |
| FR-002 per-agent cookie isolation | US-2 | window.open-stays-in-opener | `TestBrowserContext_WindowOpenIsolation` (spike) | D2 / O1 |
| FR-003 `Shutdown` never kills process | US-3 | hot-reload-no-kill | `TestManager_Shutdown_DropsContextNotProcess` | D4 invariant 2 / C1 |
| FR-004 hot-reload keeps browsing | US-3 | hot-reload-no-kill | `TestReloadProviderAndConfig_DoesNotKillChrome` (R1) | C1 |
| FR-005 crash recovery ≤10s | US-4 | crash-recovery | `TestCoordinator_CrashRecovery` (R2) | D4 / M1 |
| FR-006 global tab budget enforced | US-5 | tab-budget-denial | `TestOpenTab_GlobalBudgetDenial` | D7 / M3 |
| FR-007 no foreign-Chrome adoption | — | foreign-chrome-not-adopted | `TestCoordinator_OwnershipMarker` | D1 / M2 |
| FR-008 only Close() kills process | US-3 | (R3) | `TestClose_IsOnlyProcessKill` (R3) | D4 invariant 4 |
| FR-009 no wire-schema change | US-7 | — | `make verify-contracts` | D3 / M4 |
| FR-010 live-view per-agent | US-6 | — | `TestBrowserWS_BindsOnAgentID` | D3 |

## 10. TDD plan (ordered; Unit → Integration → E2E)

| Order | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 1 | `TestBrowserContext_WindowOpenIsolation` | Integration | FR-002 | **THE D2 spike** — gates Stream B |
| 2 | `TestCoordinator_OwnershipMarker` | Unit | FR-007 | marker write/read/verify |
| 3 | `TestCoordinator_GetOrConnect_LaunchesOnce` | Unit | FR-001 | single launch under concurrent callers |
| 4 | `TestManager_Shutdown_DropsContextNotProcess` | Unit | FR-003 | the C1 invariant |
| 5 | `TestReloadProviderAndConfig_DoesNotKillChrome` (R1) | Integration | FR-004 | the CRITICAL regression |
| 6 | `TestCoordinator_CrashRecovery` (R2) | Integration | FR-005 | launcher-wait + connectors re-ask |
| 7 | `TestClose_IsOnlyProcessKill` (R3) | Integration | FR-008 | gateway Close kills; nothing else |
| 8 | `TestOpenTab_GlobalBudgetDenial` | Unit | FR-006 | D7 |
| 9 | `TestBrowserWS_BindsOnAgentID` | Integration | FR-010 | D3 unchanged binding |
| 10 | `TestFiveAgents_ConcurrentStress` | E2E | SC-002 | the 5-agent headline (this pod) |
| 11 | `verify-contracts` | Build | FR-009 | `make verify-contracts` green |

### Regression requirements
- **Preserve:** all existing `pkg/tools/browser/*_test.go` behavior EXCEPT the single-Chrome assumption (tests that assumed one-manager-one-Chrome must move to the coordinator model); ADR-038/040/041 UAT matrix (tabs, take-the-wheel, screencast) must still pass.
- **New regression tests + assertion mechanisms (grill MAJ-005):**
  - **R1 (reload no-kill):** capture `coordinator.PID()` before `ReloadProviderAndConfig`; assert equal after. Mechanism: the coordinator's `PID()` accessor (reads the live `os/exec` handle's pid, 0 if none).
  - **R2 (crash recovery):** `coordinator.PID()` is observed dead (process not in `/proc`), then a new live pid within 10 s; assert fresh context (login absent).
  - **R3 (Close-only-kill):** instrument the coordinator with a `killCount` counter incremented only in `Shutdown()`; after a reload + a per-agent `Release`, assert `killCount==0`; after `Close()`, assert `killCount==1` and the pid is dead. This makes "Close is the ONLY kill path" a concrete positive assertion, not a vague negative.

### Test datasets
| Input | Expected | Traces to |
|---|---|---|
| 1 agent, 1 tab | succeeds; 1 context | FR-001 |
| 5 agents, 1 tab each | succeeds; 5 contexts, isolated cookies | SC-002 |
| `max_total_tabs=2`, 3rd open | denied (budget) | FR-006 |
| foreign Chrome on 9223 (no marker) | rejected, not adopted | FR-007 |
| Chrome killed mid-session | recovered ≤10s, cookies intact | FR-005 |
| ReloadProviderAndConfig mid-browse | Chrome PID unchanged; logins preserved | FR-004 |
| Same agent, 2 concurrent tool calls | both succeed, same context (no internal lock contention deadlock) | FR-001 |
| Empty context, 0 tabs, agent calls browser_get_text | clean empty/no-element result, no crash | FR-001 |
| 2 agents race `browser_open_tab` at budget boundary (max_total_tabs=2, 1 open) | exactly ONE succeeds, the other is denied | FR-006 |
| Chrome crash then immediate tool call | tool retries/queues until recovery (≤10s), then succeeds in a FRESH context (prior cookies/login NOT present — CRIT-001) | FR-005 |

## 11. Functional requirements & success criteria
- **FR-001..FR-010** as in §9 (MUST).
- **SC-001:** 2 agents browse concurrently with zero "port in use" failures over a 60s mixed-load run.
- **SC-002 (headline):** **5 agents** browse concurrently (distinct sites, mixed navigation) for ≥60s on this devpod — all isolated, live-view correct per agent, no crash, **total browsing RSS < 4 GB**. Measurement (round-2 MAJ-009): sum `VmRSS` (kB) from `/proc/<pid>/status` across the shared Chrome's process tree (the coordinator's pid + all descendants via the pgid), sampled at the 60s mark. Sized for the 3.8 GB pod floor (measured 91 MB baseline + per-tab headroom; if the pod has <8 GB free, cap tabs per agent to keep under 4 GB). (The **10-agent** claim is validated on the `ci-omnipus` 16 GB worker with RSS < 8 GB — grill O4.)
- **SC-003:** hot-reload mid-browse kills 0 Chrome processes (R1).
- **SC-004:** crash recovery ≤10 s for all agents (R2).
- **SC-005:** `make verify-contracts` green (no wire-schema change).

## 12. Ambiguity self-audit (resolved inline per operator directive — documented as assumptions)
| Ambiguity | Resolution (assumption, recorded) |
|---|---|
| Coordinator placement (gateway pkg vs browser pkg) | **DECIDED (round-2):** `pkg/tools/browser/coordinator.go` — same package as `BrowserManager` (avoids an import cycle; the coordinator now OWNS the contexts, so it must live with the manager). Load-bearing given CRIT-003. |
| Recovery bound T | **Assume:** ≤10 s (SC-004); tunable. |
| `max_total_tabs` default | **Assume:** 30 (sized from the measured 91 MB baseline; D7). Tunable. |
| 10-simultaneous validation host | **Assume:** `ci-omnipus` 16 GB worker (O4); this pod validates 5. |
| Whether `adoptTarget` works on a child context | **Resolve by the D2 spike (test #1)** — chromedp docs say `WithTargetID` on a `WithNewBrowserContext` *child* is safe; only combining both on the same context panics. Verify, don't assume. |

## 13. Holdout evaluation scenarios (post-implementation, NOT in TDD/traceability)
1. **(happy)** 5 custom agents each navigate a different real site, scroll, screenshot — all 5 return distinct screenshots; no errors over 2 min.
2. **(happy)** Operator opens the live panel for agent #3 mid-run — sees #3's page, not #1/#2's.
3. **(error)** Kill the Chrome process from a shell while 3 agents browse — all 3 resume browsing within ~10s without manual action.
4. **(error)** Set `max_total_tabs=2`, open 2 tabs, a 3rd agent's `browser_open_tab` is refused with a clear message.
5. **(edge)** Save an unrelated Setting (e.g., a display toggle) while 3 agents browse — browsing continues; the Chrome PID is unchanged.
6. **(edge)** Two agents log into the same site with different accounts in their own contexts — each stays logged into its own account.
7. **(edge)** An unrelated Chrome is already on 9223 at gateway start — the gateway does not drive it; a clear error or a distinct managed instance results.

---

**Next:** two grill-spec rounds on this spec (`/grill-spec docs/internal/specs/browser-shared-chrome-spec.md`), then implement in the fan-out waves (A critical-path first, then B/C/D parallel), then the 7-reviewer + intensive critical review, fix, and the 5-agent stress test (SC-002).
