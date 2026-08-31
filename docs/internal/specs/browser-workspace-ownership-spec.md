# Spec — Browser ownership: workspace-scoped browsing contexts (ADR-072 **D1**)

- **Source ADR:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions.md` — **D1 sections only (D1.0–D1.5)**, plus the write-lease decision the ADR files under D2.10 but §4 attributes to D1 (see §12, ambiguity A7).
- **Round-1 review folded in:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions-review.md` (26 findings; C1–C5, M2–M7, m5, O2, O3 all land in D1's territory).
- **Amends:** **ADR-043 D2** (per-agent CDP browser context) and **ADR-043 D3** (live-view binding). Read ADR-043 before implementing.
- **Sibling spec:** D2 (capability — AX selection, actionability, missing verbs, `browser_snapshot`) is specced separately. **This spec owns the write lease; the D2 spec must not re-specify it.**
- **Worktree:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf` · **Branch:** `feat/browser-streaming-performance`
- **Status:** Draft for grill-spec → implementation
- **Operator rulings folded in (2026-08-31):** workspace is the isolation axis, not the agent and not the conversation; unattended delegated work starts signed out; every turn runs in a workspace (no workspace-less fallback); the browser seed stays Jim + Ray only.

---

## 1. Overview / Actors / Scope

**Problem.** Signed-in browser state is keyed by the **agent**, so it strands the moment the operator switches who they are talking to. `AgentLoop.browserMgrs` is `map[agentID]*browser.BrowserManager` (`pkg/agent/loop.go:279`), one manager per agent, created and re-keyed by agent id at `pkg/agent/loop.go:2473` (`mgr.AttachSharedChrome(coordinator, agentID)`) and `:2551`. Each manager holds exactly **one** coordinator-owned CDP browser context (`pkg/tools/browser/manager.go:381`, applied to every session at `manager.go:1369`), and every browser tool addresses its tab set through one hardcoded key, `DefaultSessionID = "default"` (`pkg/tools/browser/tools.go:63`). The operator browses with Mia, switches the chat to Jim, and Jim — correctly, for his own manager — reports zero tabs while telling the operator the browser is "shared across the workspace", because six tool descriptions say exactly that (`pkg/tools/browser/tabs.go:32,86,143,206`; `tools.go:415`; plus two Go comments at `tabs.go:19,186`).

**Solution (ADR-072 D1).** Move the isolation axis from the **agent** to the **workspace**. The isolation primitive is unchanged — one CDP browser context per unit, created and owned by `BrowserCoordinator` (`pkg/tools/browser/coordinator.go:105,311`) — only its key changes. A login obtained in workspace X stays invisible in workspace Y; every agent on one workspace's team shares it; a new chat in the same workspace does not log out. An **unattended delegated sub-turn** is deliberately excluded: it gets its own context keyed by transcript session id and therefore starts signed out.

**Actors:**
- `AgentLoop` (`pkg/agent/loop.go`) — owns `browserMgrs`, the coordinator, and turn context injection (`:7968` session key, `:7972` transcript session id, `:7988` workspace id).
- `BrowserCoordinator` (`pkg/tools/browser/coordinator.go`) — owns the shared Chrome and every CDP browser context; `Register` (`:311`), `Release` (`:514`), `RemoveAgent` (`:542`), `disposeBrowserContextRaw` (`:585`).
- `BrowserManager` (`pkg/tools/browser/manager.go`) — owns the connection, the `sessions` map (`:338`), the single `browserCtxID` (`:381`), the viewer counters (`:2812`, `:2830`) and the reaper (`:2986`).
- The eleven browser tools (`pkg/tools/browser/register.go:65-81`) — every one passes a session id today and every one passes the constant.
- The gateway live surfaces — `browser_webrtc.go:279`, `browser_ws.go:1252`, `browser_inspect.go:73`, all `BrowserManagerForAgent(frame.AgentId)`.
- `workspace.FindForAgentPreferring` (`pkg/workspace/find_for_agent.go:176`) — the existing resolution ladder `pkg/tools/resolvepath.go:713` already uses for the same gap.

**In scope (D1):**
- One `BrowserManager` per **workspace** (D1.1 change 1) and per **unattended delegated sub-turn** (D1.2).
- Browsing contexts keyed by the resolved workspace id instead of `"default"` (D1.1 change 2).
- Workspace resolution ladder with **no constant fallback** and a named failure (D1.4).
- Three-state `browser_list_tabs` observability (D1.5) + the six description strings.
- Per-browsing-context **write lease** for action tools (ADR D2.10; a D1 consequence per ADR §4).
- Gateway server-side agent→workspace resolution with **no wire field added** (ADR-043 D3 amendment).
- Audit event on browsing-context creation and on first cross-agent use (ADR D2.11 repudiation bullet — a D1-caused requirement).

**Out of scope (explicitly):**
- Everything in **D2** — role/accessible-name selection, actionability, `browser_select_option` / `browser_press_key` / `browser_hover` / `browser_upload_file` / dialog handling / `browser_snapshot`, tier assignment (D2.8), the D2 policy seeding (D2.9), the D2.11 information-disclosure bullet.
- Mid-tool preemption and sustained-contention fairness beyond FR-023's bounded wait (ADR §6, open).
- Re-keying the `web_serve` preview URL (ADR §6, open; `/preview/<agent>/<token>/` stays agent-scoped).
- Changing the seeded browser policy roster. Jim (`pkg/coreagent/core.go:1052-1064`) and Ray (`:910-921`) keep it; Mia (`:848`) and Ava (`:794`) stay deny-by-default. Operator-confirmed 2026-08-31.
- Migrating existing on-disk per-agent state (see §12, ambiguity A9 — the decision is *discard*, not merge).

---

## 2. Existing Codebase Context

### Symbols involved

| Symbol | Role | Context (verified) |
|---|---|---|
| `AgentLoop.browserMgrs` (`pkg/agent/loop.go:279`) | **modifies** | `map[string]*browser.BrowserManager`, keyed by agent id today (comment at `:251`) → keyed by **browsing-context key** |
| `AgentLoop.BrowserManagerForAgent` (`loop.go:4871`) | **modifies** | the gateway's only resolver; three call sites |
| `AgentLoop.BrowserManagers()` (`loop.go:4887`) | **reuses** | snapshot slice for reaping/Close; semantics unchanged, membership changes |
| browser registration block (`loop.go:2289-2570`) | **modifies** | `:2289` "Tools are always registered; whether an agent can actually invoke them is determined by the policy engine" — **load-bearing for FR-014**; `:2473` `AttachSharedChrome(coordinator, agentID)`; `:2550-2551` prior/replace; `:2857-2866` prune + `coord.RemoveAgent(id)` |
| `BrowserManager.browserCtxID` (`manager.go:381`) | **modifies** | a **single** field per manager, consumed at `manager.go:1369` (`WithExistingBrowserContext`) for every session it bootstraps. This is why FR-011 exists |
| `BrowserManager.sessions` (`manager.go:338`) | **modifies** | `map[string]*sessionEntry`; today one entry under `"default"` |
| `BrowserManager.AttachSharedChrome` (`manager.go:537`) | **modifies** | sets `m.agentID` (`:375`), the coordinator's Register/Release/RemoveAgent key |
| `BrowserManager.ListTabs` (`manager.go:1605`) | **modifies** | `return nil, 0, nil` on missing session (`:1609-1611`) — the two-state collapse |
| `BrowserManager.sessionExists` (`manager.go:2378`) | **reuses** | already backs `browser_started` — the existing half of D1.5 the ADR does not mention |
| `BrowserManager.ViewerAttached/ViewerDetached` (`manager.go:2812,2830`) | **extends** | `se.viewers` is the only viewer signal; **no exported count accessor exists** → FR-010 adds one |
| `BrowserManager.ReapIdleSessions` (`manager.go:2986`) | **no change** | per-tab TTL + `se.viewers > 0` pin + zero-tab `emptySince` branch, all already implemented |
| `BrowserCoordinator.Register` (`coordinator.go:311`) | **modifies** | `(ctx, agentID, mgr) → (rootCtx, browserCtxID, err)`; `agentID` becomes the browsing-context key |
| `BrowserCoordinator.RemoveAgent` (`coordinator.go:542`) | **modifies** | sole disposal path; called from `loop.go:2866` |
| `controlledResult` (`tools.go:962`) | **extends** | 7 gated action tools (`tools.go:119,232,429,879`; `tabs.go:113,171,239`); read-only screenshot/get_text/wait ungated |
| `ListTabsTool` (`tabs.go:28-68`) | **modifies** | already returns `browser_started` (`:58`, `:66`); description at `:31-39` |
| `tools.ToolWorkspaceID` (`pkg/tools/base.go:250`) | **reuses** | set only when `ts.opts.WorkspaceID != ""` (`loop.go:7988`) |
| `tools.ToolTranscriptSessionID` (`base.go:200`) | **reuses** | set at `loop.go:7972`; child's own id via `subturn.go:1282` |
| `tools.ToolDelegationDepth` (`base.go:292`) | **extends** | exists, but is set **only** by `task_executor.go:874,2693` — **not** by `spawnSubTurn` → FR-010 |
| `spawnSubTurn` `WorkspaceID` (`subturn.go:1323`) | **no change** | `WorkspaceID: parentTS.opts.WorkspaceID` — the child **inherits** the parent's workspace; this is why FR-009 needs its own discriminator |
| `workspace.FindForAgentPreferring` (`find_for_agent.go:176`) | **reuses** | preferred-id fast path → `FindForAgent` (`:83`) sorted-first tie-break + WARN |
| `pkg/tools/resolvepath.go:713` | **reuses** | the precedent: same gap, same ladder, "so the two never disagree about which workspace this turn is rooted in" |
| `session.UnifiedMeta.WorkspaceID` (`pkg/session/unified_meta_files.go:60`) | **reuses** | `json:"workspace_id"` — the gateway's preferred id for FR-017 |
| `pkg/agent/tool_denial.go:206-210` | **reuses** | `policy_denied` → ModelMessage "Tool execution denied by policy." — the third state's actual producer |
| `BrowserAttachFrame.yaml`, `BrowserWebRTCOfferFrame.yaml`, `BrowserInspectRequest.yaml` | **modifies (prose only)** | descriptions assert "isolated per-agent browser contexts" / "agent_id is the binding key" |

### Impact assessment

| Symbol modified | Risk | Direct dependents (d=1) | Indirect (d=2) |
|---|---|---|---|
| `browserMgrs` keying | **CRITICAL** | `BrowserManagerForAgent`, `BrowserManagers`, reload prune (`loop.go:2857`), `Close` (`loop.go:3957`) | all 3 gateway surfaces, every browser tool, reaping |
| `BrowserManager.browserCtxID` → per-key map | **CRITICAL** | `bootstrapBrowserCtx` (`manager.go:1369`), `Session` (`:1022`), `dropConnection`/`invalidateConnection` (`:3306`, `:3424`) | ADR-043 CRIT-002/CRIT-003 invariants, cookie survival across reload |
| `Coordinator.Register` key | **HIGH** | `ensureStarted` (`manager.go:771`), `Release`, `RemoveAgent` | tab budget bookkeeping, crash recovery |
| session-id argument at every tool | **HIGH** | 9 call sites in `tools.go`, all of `tabs.go` | live view attach, screencast target |
| `ListTabs` return shape | MEDIUM | `ListTabsTool` (`tabs.go:59`), `browser_inspect` | model-visible answer — the §1.1 defect |
| gateway `BrowserManagerForAgent` | **HIGH** | `browser_webrtc.go:279`, `browser_ws.go:1252`, `browser_inspect.go:73` | WebRTC capture registry keyed by agent id (`browser_webrtc.go:77`) |
| `controlledResult` + lease | **HIGH** | 7 action tools | ADR-038 D6 take-the-wheel |

**Verified numeric corrections to the ADR (review m5, M8):** the phrase "the shared browser session" occurs **8** times in the tree, of which **5** are model-visible (`tabs.go:32,86,143,206` descriptions + `tools.go:415` parameter description), **2** are Go comments (`tabs.go:19,186`) and **1** is an unrelated SPA comment (`src/store/ui.ts:135`). The ADR says "six tool descriptions". Use the enumerated list, not the count.

---

## 3. Implementation Streams (fan-out for parallel agents)

Seven streams. **Stream A is the critical path and must land its interface first**; B–F code against that interface.

### Shared interface contract (Stream A's first commit — everyone else codes against this)

```go
// pkg/tools/browser/key.go (new)

// BrowsingKey is the identity of ONE cookie jar: one CDP browser context, one
// BrowserManager, one tab set. It replaces the DefaultSessionID constant as the
// thing every browser tool addresses. Constructed ONLY by ResolveBrowsingKey —
// there is deliberately no exported literal constructor and no zero-value
// default, so a caller cannot accidentally mint a shared jar (ADR-072 D1.4).
type BrowsingKey struct{ s string }

func (k BrowsingKey) String() string { return k.s }
func (k BrowsingKey) IsZero() bool   { return k.s == "" }

// Kind distinguishes the two legal shapes, for audit + logging only. Never a
// branch in the isolation logic itself.
type BrowsingKeyKind int
const (
    BrowsingKeyWorkspace BrowsingKeyKind = iota // "ws:<workspaceID>"  — D1.1
    BrowsingKeyUnattended                        // "un:<transcriptID>" — D1.2
)
func (k BrowsingKey) Kind() BrowsingKeyKind

// ErrNoBrowsingContext is the D1.4 named failure. It MUST be returned — never
// swallowed into a shared jar, never mapped to a constant, never nil-with-empty.
// Its Error() text is a behavioral contract (FR-008): it names the agent and
// says the turn is not rooted in a workspace.
var ErrNoBrowsingContext = errors.New(
    "browser: this turn is not rooted in a workspace, so it has no browsing context of its own; " +
        "add this agent to a workspace's team, or run the request in a workspace chat")
```

```go
// pkg/tools/browser/resolve.go (new) — the SINGLE resolution point.

// ResolveBrowsingKey decides which cookie jar this turn's browser tools address.
// It is the ONLY function permitted to construct a BrowsingKey. Deterministic,
// pure apart from the workspace-file read FindForAgentPreferring performs.
//
// Ladder (ADR-072 D1.2 + D1.4), evaluated in order:
//   1. UNATTENDED DELEGATED SUB-TURN  -> BrowsingKeyUnattended(transcriptSessionID)
//      Conditions, BOTH required: tools.ToolSubTurn(ctx) is true, AND the
//      candidate workspace jar has zero attached viewers (Viewers()==0).
//      A sub-turn WITH a viewer attached is attended and falls through to (2).
//   2. tools.ToolWorkspaceID(ctx) != ""       -> BrowsingKeyWorkspace(that id)
//   3. workspace.FindForAgentPreferring(home, tools.ToolAgentID(ctx), "")
//      found                                  -> BrowsingKeyWorkspace(resolved id)
//   4. otherwise                              -> zero key + ErrNoBrowsingContext
//
// Step 3 is the pkg/tools/resolvepath.go:713 precedent, verbatim, so the browser
// and the work dir never disagree about which workspace a scheduled/heartbeat
// turn is rooted in. There is NO step 5. A fallback constant here re-creates the
// exact isolation regression review finding C3 identified.
func ResolveBrowsingKey(ctx context.Context, home string, viewers ViewerCounter) (BrowsingKey, error)

// ViewerCounter is the seam step 1 needs: "does anyone have the live panel open
// on this workspace's context right now?" Implemented by *AgentLoop over its
// managers; faked in unit tests so step 1 is testable without Chrome.
type ViewerCounter interface{ Viewers(k BrowsingKey) int }
```

```go
// pkg/agent/loop.go — manager lookup replaces the per-agent map.

// BrowserManagerForKey returns (creating on first use) the manager that owns
// key's browsing context. Exactly one manager per key, process-wide.
func (al *AgentLoop) BrowserManagerForKey(k browser.BrowsingKey) (*browser.BrowserManager, error)

// BrowserManagerForAgent is RETAINED for the gateway (FR-016: no wire change).
// It resolves agentID -> BrowsingKey server-side using preferredWorkspaceID
// (from the attaching chat session's meta, FR-017) and then delegates to
// BrowserManagerForKey. Returns ok=false rather than a shared jar when the
// agent resolves to no workspace.
func (al *AgentLoop) BrowserManagerForAgent(agentID, preferredWorkspaceID string) (*browser.BrowserManager, bool)
```

```go
// pkg/tools/browser/manager.go — the manager stops being single-context.

// browserCtxIDs replaces the single browserCtxID field (manager.go:381).
// One manager owns one key, so this is a one-entry map in production; it is a
// map rather than a field so the CRIT-003 invariant ("no manager path ever
// calls WithNewBrowserContext") stays checkable and so a manager can be proven
// to refuse a session id that is not its own key.
//   INVARIANT M-1: m.sessions and m.browserCtxIDs have identical key sets.
//   INVARIANT M-2: Session(id) errors if id != m.key.String(); a manager NEVER
//                  lazily creates a jar for a key it does not own.

// Viewers reports how many live-panel viewers are attached to this manager's
// browsing context. Reads se.viewers (manager.go:2818/2836) under m.mu.
func (m *BrowserManager) Viewers() int

// TabState is the D1.5 three-state answer. It is a CLOSED enum; adding a state
// is a spec change, not an implementation detail.
type TabState int
const (
    TabStateNoContext TabState = iota // no browsing context for this key yet
    TabStateOpen                      // a live context; len(tabs) >= 1
    TabStateEmpty                     // a live context that momentarily has 0 tabs
)

// ListTabsState replaces ListTabs' (nil, 0, nil) collapse (manager.go:1609).
// ListTabs itself is KEPT, delegating to this, so non-tool callers are unchanged.
func (m *BrowserManager) ListTabsState(sessionID string) (TabState, []Tab, int, error)
```

```go
// pkg/tools/browser/lease.go (new) — ADR D2.10, one writer per browsing context.

// writeLease is a per-BROWSING-CONTEXT (not per-manager-mutex) mutual exclusion
// held for the duration of ONE action tool call. It is NOT m.mu and must never
// be taken while m.mu is held (ADR-038 no-lock-across-blocking-call: an action
// tool blocks on CDP for seconds).
//
// acquireWrite returns:
//   ok=true                 -> caller holds the lease; MUST defer release()
//   ok=false, holder="jim"  -> caller must return deferredResult(...) — a
//                              NON-error {"deferred":true,"reason":...}
// It waits up to leaseWaitTimeout before returning ok=false, so a lease held by
// a fast action is not reported as contention (FR-023).
func (m *BrowserManager) acquireWrite(ctx context.Context, sessionID, holderAgentID string) (release func(), ok bool, holder string)
```

**Locking discipline (load-bearing).** Order is `writeLease → m.mu`, never the reverse, and `m.mu` is never held across `acquireWrite`. `acquireWrite` is cancellable by `ctx` so a cancelled turn does not park a goroutine. `release()` is idempotent and MUST run via `defer` in every action tool so a panic or a CDP timeout cannot wedge the context (FR-024).

### Stream A — Key + resolution + manager re-key [CRITICAL PATH]
**Owns:** `key.go`, `resolve.go` (new); `BrowserManager.browserCtxIDs` + `Viewers()` + invariants M-1/M-2; `AttachSharedChrome`'s key parameter; `BrowserCoordinator.Register/Release/RemoveAgent` key rename; `AgentLoop.BrowserManagerForKey` and the `browserMgrs` re-key (`loop.go:279,2473,2551,2857-2866,3957`).
**Depends on:** nothing. **Interface out:** the contract above.
**Invariants:** (1) no code path outside `ResolveBrowsingKey` constructs a `BrowsingKey`; (2) no `WithNewBrowserContext` on any manager path (ADR-043 CRIT-003, unchanged); (3) `RemoveAgent`/`disposeBrowserContextRaw` remain the sole disposal path; (4) reload still does connection-only teardown (ADR-043 CRIT-002) — cookies survive a Settings save, now per workspace.

### Stream B — The unattended split (D1.2) [DEPENDS ON A]
**Owns:** the `spawnSubTurn` discriminator. `subturn.go` must stamp the child turn as a sub-turn on the tool context; `loop.go:7960-7990` must inject it. **This is new plumbing** — `ToolDelegationDepth` (`base.go:292`) exists but is set only by `task_executor.go:874,2693` and is 0 for a `delegate`-spawned sub-turn, so it cannot be reused as-is (§12, A2). Also owns the login-wall failure text (FR-012).
**Interface out:** `tools.WithSubTurn(ctx, true)` / `tools.ToolSubTurn(ctx) bool`, mirroring the `WithDelegationDepth`/`ToolDelegationDepth` pair exactly.
**Load-bearing:** the unattended jar must be a **separate CDP browser context**, obtained through `Coordinator.Register(ctx, key, mgr)` with the unattended key — not a second entry in an existing manager's `sessions` map. A second `sessions` entry would reuse the same `browserCtxID` (`manager.go:1369`) and the sub-turn would be **signed in**, silently failing FR-009 and ADR criterion 17.

### Stream C — Three-state tabs + descriptions (D1.5) [DEPENDS ON A]
**Owns:** `ListTabsState` + `ListTabs` delegation (`manager.go:1605-1613`); `ListTabsTool.Execute` (`tabs.go:48-68`) wire shape; the five model-visible description strings (`tabs.go:32,86,143,206`, `tools.go:415`) and the two comments (`tabs.go:19,186`).
**Does NOT own:** the "not permitted" state. That is produced by the policy layer (`tool_denial.go:206-210`) and can never be returned by a tool that policy prevented from running. Stream C's job is to ensure the two states it *can* produce are never confusable with it (FR-013/FR-014).

### Stream D — Write lease (ADR D2.10) [DEPENDS ON A]
**Owns:** `lease.go`; the `acquireWrite`/`release` call pairs in the 7 action tools (`tools.go:119,232,429,879`; `tabs.go:113,171,239`); composition with `controlledResult` (`tools.go:962`).
**Composition order (fixed):** `controlledResult` first (a human holding the wheel outranks an agent queue — ADR-038 D6), then `acquireWrite`. Both produce the same `{"deferred": true, "reason": …}` non-error shape, with different `reason` text.
**Non-goal:** read-only tools (`browser_screenshot`, `browser_get_text`, `browser_wait`) stay ungated, exactly as today.

### Stream E — Gateway resolution + contracts prose [DEPENDS ON A]
**Owns:** the three call sites (`browser_webrtc.go:279`, `browser_ws.go:1252`, `browser_inspect.go:73`); server-side agent→workspace resolution preferring the attaching session's `workspace_id` (`pkg/session/unified_meta_files.go:60`); the WebRTC capture registry's keying (`browser_webrtc.go:77`, keyed by agent id today); the `browser-webrtc[<agent>]` log label (cosmetic — keep the agent label, per ADR D1.0 and review O3); description-only edits to `BrowserAttachFrame.yaml`, `BrowserWebRTCOfferFrame.yaml`, `BrowserInspectRequest.yaml`.
**Contract gate:** **no field, enum or requiredness change.** `make verify-contracts` must stay green after `make gen-contracts`. If implementation discovers a wire field is genuinely required, **stop** — that is the 5-step Hard-Constraint-#8 process and a spec amendment, not a code change (§12, A6).

### Stream F — Audit + lifecycle [DEPENDS ON A]
**Owns:** the audit event on browsing-context creation and on an agent's first use of a context it did not establish (FR-027); disposal on workspace deletion and on roster change (FR-026, via the `loop.go:2866` `RemoveAgent` hook); the reaping interactions (FR-025 — assert, do not rewrite: `ReapIdleSessions` at `manager.go:2986` already does per-tab TTL, the `se.viewers > 0` pin and the zero-tab `emptySince` branch).

### Stream G — Tests + regression (cross-cutting)
**Owns:** §10's ordered plan and §10's regression list. Runs alongside every stream.

**Parallelization:** A lands its interface first. B/C/D/E/F then fan out on disjoint files (`subturn.go`+`base.go` · `tabs.go`+`manager.go` tab paths · `lease.go`+tool call sites · `pkg/gateway/` + `contracts/` · audit + lifecycle). G is continuous.

---

## 4. Behavioral contract (observable)

1. **Handover.** Given the operator browsed in a workspace chat with Mia and switches the chat to Jim, when Jim calls `browser_list_tabs`, he sees the same tabs, with no handover command and no re-navigation.
2. **Human-first.** Given the operator opens the live panel and browses before addressing any agent, when they then ask any browser-policy-allowed agent on that workspace what is open, that agent sees the tab.
3. **Cross-workspace isolation.** Given a site login established in workspace X, when the same site is opened in workspace Y, Y is logged out.
4. **No surprise logout.** Given a site login established in one chat, when a **new chat in the same workspace** opens the same site, it is still logged in.
5. **Unattended sub-turn.** Given a delegated sub-turn running with no viewer attached, when it opens a site the operator is signed into on that workspace, it is **logged out**; and when it hits a login wall, the failure text names the reason ("this ran unattended and has no signed-in session").
6. **Attended sub-turn.** Given a delegated sub-turn running while a viewer *is* attached to the workspace's context, when it browses, it uses the **workspace** context (delegation alone does not isolate — absence of a watcher does).
7. **Workspace-less turn.** Given a scheduled or heartbeat turn whose `ToolWorkspaceID(ctx)` is empty but whose work dir was re-rooted into a CoreTeam workspace, when it calls a browser tool, it reaches **that same workspace's** browsing context.
8. **Genuine no-workspace.** Given an agent on no workspace at all, when it calls any browser tool, the tool **fails with `ErrNoBrowsingContext`'s named text** — never a shared context, never an empty success.
9. **Three states.** `browser_list_tabs` returns a payload from which "this workspace has no browsing context yet", "there is a context and here are its tabs" and "there is a context that momentarily has none" are each distinguishable without inference.
10. **Not permitted.** Given a policy-denied agent (Mia, Ava on the seeded roster), when asked what is open in the browser, the answer the model can produce is "denied by this agent's policy" — never "there are no tabs".
11. **One writer.** Given two agents on one workspace issuing action tools concurrently against the same context, neither observes the other's mid-action state; the loser receives a **non-error** `{"deferred": true, "reason": …}` and no tool errors.
12. **Human outranks agent.** Given a human holds the live-view control lock, an agent action tool still defers with the ADR-038 D6 reason, not the lease reason.
13. **No wedge.** Given an action tool panics, times out or has its context cancelled while holding the lease, the next action tool on that context acquires it.
14. **Live panel.** Given any of the three gateway surfaces receives `agent_id`, the manager it resolves is the one that owns the browsing context that agent's turns use for the attaching chat session.
15. **Reload.** Given a Settings save mid-browse, the shared Chrome pid is unchanged and each **workspace's** context is re-adopted with login intact (ADR-043 CRIT-002, re-keyed).
16. **Audit.** Given a browsing context is created, or an agent uses one it did not establish, an audit event records which agent, which context key and which workspace.

---

## 5. Explicit non-behaviors

- The system must **not** fall back to `DefaultSessionID`, `""`, the agent id, or any other constant when workspace resolution fails. There is no default jar. (Review C3.)
- The system must **not** give an unattended delegated sub-turn a second `sessions` entry inside a workspace manager — that shares the cookie jar and silently defeats FR-009.
- The system must **not** treat "delegated" alone as "unattended". Attendance is decided by viewer count, per D1.2's own wording.
- The system must **not** return `nil, 0, nil` from `ListTabs` for a missing context (`manager.go:1609-1611`) once `ListTabsState` exists.
- The system must **not** add, remove or retype any field in `contracts/` for D1. Description prose only.
- The system must **not** widen the seeded browser tool policy. Mia and Ava stay denied.
- The system must **not** call `chromedp.WithNewBrowserContext` on any `BrowserManager` path (ADR-043 CRIT-003, unchanged).
- The system must **not** hold `m.mu` across `acquireWrite` or across any CDP call (ADR-038 discipline).
- The system must **not** dispose a browsing context on hot reload; only on workspace deletion, roster removal, reaping, or gateway `Close()`.
- The system must **not** change the `browser-webrtc[<agent>]` log label to a workspace label — the label is cosmetic and the agent is still the useful identity in a log line (review O3).
- The system must **not** re-key the `web_serve` preview URL. Out of scope, ADR §6 open.

---

## 6. Integration boundaries

- **CDP / chromedp** (`target.CreateBrowserContext`, `disposeBrowserContext`, `WithExistingBrowserContext`): in-process. A create failure surfaces as a tool error naming the workspace, never as a silent join to another context. The count of live contexts now scales with **workspaces + concurrently-unattended sub-turns**, not agents — fewer and longer-lived on the workspace axis, but the sub-turn axis is new and is what FR-025's reaping must actually bound.
- **Workspace store** (`pkg/workspace/find_for_agent.go`): read-only, file-scanning. `FindForAgent` tie-breaks multi-membership by sorted-first id and logs a WARN (`find_for_agent.go:83`'s doc comment); `FindForAgentPreferring`'s fast path **suppresses** that WARN. FR-018 depends on both the turn and the gateway supplying the same preferred id so the tie-break never has to arbitrate differently for the same operator action.
- **Session store** (`pkg/session/unified_meta_files.go:60`): the gateway reads `workspace_id` off the attaching chat session's meta. A session with no `workspace_id` degrades to `FindForAgentPreferring(home, agentID, "")` — same ladder, same tie-break.
- **Policy engine** (`pkg/agent/tool_denial.go:206-210`, `loop.go:2742`): unchanged. It is the producer of the third `browser_list_tabs` state; D1 only guarantees the other two cannot be mistaken for it.
- **Audit** (`pkg/audit`): two new event types. Existing severity/format conventions; no new sink.
- **SPA**: no wire change, so no required SPA change. The `src/store/ui.ts:135` comment wording is cosmetic and may be corrected in the same PR.
- **Sandbox**: unchanged. No new ports, no new filesystem roots, no new exec.
- **Platform (Windows/POSIX):** D1 introduces **no** platform-conditional behaviour. The write lease is an in-process `sync` primitive, real on every platform; it deliberately does **not** use `fileutil.WithFlock`, which is a documented no-op on Windows (`pkg/fileutil/flock_windows.go`). Chrome/CDP behaviour is identical across platforms. The pre-existing macOS/Windows sandbox posture is untouched.

---

## 7. User stories & acceptance criteria

**US-1 (P0) Agent handover.** As an operator, when I switch the chat from one agent to another mid-session, I want the new agent to see and drive the browser I was just using, with no handover step.
- *Why P0:* the reported defect (ADR §1.1); ADR criterion 2.
- *Independent test:* browse as Mia in workspace W; ask Jim (browser-allowed) to list tabs → same tab set.
- **AC1: Given** a workspace W with a browsing context holding one tab, **When** an agent on W's team with `browser_list_tabs: allow` calls it, **Then** that tab is returned regardless of which agent opened it.
- **AC2: Given** the same, **When** the operator issues no handover command of any kind, **Then** AC1 still holds.

**US-2 (P0) Human browses first.** As an operator, I want to open the browser panel and browse before deciding which agent to ask, and still have an agent take over.
- **AC1: Given** the operator browsed via the live panel in workspace W before addressing any agent, **When** a browser-allowed agent on W is asked what is open, **Then** it sees the operator's tab.

**US-3 (P0) Cross-workspace isolation survives the re-key.** As an operator with two clients in two workspaces, I want a login in one to be invisible in the other.
- *Why P0:* this is the ADR-043 D2 guarantee being **moved, not deleted**; ADR criterion 5b.
- **AC1: Given** a site login established in workspace X, **When** the same site is opened from workspace Y, **Then** Y is logged out **and** X's and Y's `cdp.BrowserContextID`s differ.

**US-4 (P0) No surprise logout.** As an operator, I want a new chat in the same workspace to still be logged in.
- **AC1: Given** a login established in chat C1 of workspace W, **When** a new chat C2 in W opens the same site, **Then** it is still logged in **and** C1's and C2's context ids are identical. (ADR criterion 5c.)

**US-5 (P0) Unattended background work is signed out.** As an operator, I do not want a process nobody is watching acting as me on a live site.
- **AC1: Given** a delegated sub-turn with zero viewers attached to W's context, **When** it opens a site the operator is signed into on W, **Then** it is logged out **and** its context id differs from W's.
- **AC2: Given** the same sub-turn hits a login wall, **When** it reports, **Then** the failure text contains the reason ("unattended", "no signed-in session"), not a bare navigation error.
- **AC3: Given** a delegated sub-turn running **while a viewer is attached** to W's context, **When** it browses, **Then** it uses W's context (still signed in) — delegation alone does not isolate.

**US-6 (P0) A workspace-less turn resolves, never merges.** As an operator running scheduled work, I want a heartbeat turn's browser to be the same workspace's browser as its files.
- *Why P0:* review C3 — the isolation regression this design must not introduce.
- **AC1: Given** a scheduled turn whose `ToolWorkspaceID(ctx)` is `""` and whose work dir was re-rooted into workspace W, **When** it calls a browser tool, **Then** it reaches W's browsing context — the same id `FindForAgentPreferring` gave `resolvepath.go:713`.
- **AC2: Given** two workspace-less agents on **no** workspace, **When** each logs into the same site, **Then** neither sees the other's session — because **both calls fail** with `ErrNoBrowsingContext` rather than sharing a jar.

**US-7 (P0) Three tab states.** As an agent, I need to tell "no browser here yet" from "a browser with nothing open".
- **AC1: Given** no browsing context for the resolved key, **When** `browser_list_tabs` runs, **Then** the payload carries `state: "no_context"` and the model-visible text says so.
- **AC2: Given** a live context with ≥1 tab, **Then** `state: "open"` with the tabs.
- **AC3: Given** a live context that momentarily has 0 tabs, **Then** `state: "empty"` — distinct from AC1.
- **AC4:** the three `state` values are the complete closed set; no fourth value is emitted for any input.

**US-8 (P1) A denied agent says it is denied.** As an operator, I want Mia to tell me she is not allowed to see the browser, not that there are no tabs.
- **AC1: Given** an agent whose policy denies `browser_list_tabs`, **When** it attempts the call, **Then** it receives "denied by this agent's policy" / "Tool execution denied by policy." and **no** tab payload.
- **AC2: Given** the same, **Then** no code path can produce a `no_context` or `empty` payload for it — the tool never runs.

**US-9 (P0) Two writers, one context.** As an operator with two agents on one workspace, I want concurrent browser work not to corrupt a page or error.
- **AC1: Given** two agents on W issuing `browser_navigate` concurrently, **When** both run, **Then** neither observes the other's mid-navigation state, neither returns `IsError=true`, and exactly one gets `{"deferred": true, …}`.
- **AC2: Given** a human holds the live-view control lock, **When** an agent issues an action tool, **Then** the deferral reason is ADR-038 D6's human-control text, not the lease text.
- **AC3: Given** an action tool panics or is cancelled while holding the lease, **When** the next action tool runs on that context, **Then** it acquires the lease within `leaseWaitTimeout`.
- **AC4: Given** any read-only tool (`browser_screenshot`, `browser_get_text`, `browser_wait`), **When** another agent holds the lease, **Then** it runs normally and is never deferred.

**US-10 (P1) Live panel keeps working, no wire change.** As a maintainer, the contract must not break.
- **AC1: Given** the implementation, **When** `make verify-contracts` runs, **Then** exit 0 with only description prose changed in the three browser schemas.
- **AC2: Given** an attach frame carrying `agent_id` and `session_id`, **When** the gateway resolves a manager, **Then** it is the manager for the browsing key that agent's turns use for that chat session.

**US-11 (P1) An agent on two workspaces is not ambiguous in practice.** As an operator, the panel and the agent must show the same browser.
- **AC1: Given** an agent on the CoreTeam of workspaces A and B, **When** the operator chats in a session stamped `workspace_id=B` and opens the panel, **Then** both the turn's tools and the panel resolve to B — not to `FindForAgent`'s sorted-first pick.

**US-12 (P1) Memory stays bounded; contexts get disposed.** As an operator on a sized host, workspace-keying must not leak contexts.
- **AC1: Given** all tabs in a workspace context idle past `IdleTTL` with no viewer attached, **When** the reaper sweeps, **Then** its tabs are reaped (per-tab, unchanged).
- **AC2: Given** one viewer attached in one chat of workspace W, **When** the reaper sweeps, **Then** W's whole context survives — the documented, accepted consequence of the re-key.
- **AC3: Given** a workspace is deleted, or an agent is removed from every workspace, **When** the reload prune runs (`loop.go:2857-2866`), **Then** the orphaned browsing context is disposed via `RemoveAgent`/`disposeBrowserContextRaw`.
- **AC4: Given** an unattended sub-turn's context, **When** the sub-turn ends and its tabs go idle, **Then** it is reaped like any other.

**US-13 (P1) Repudiation.** As an operator, I must be able to answer "which agent acted as the signed-in user".
- **AC1: Given** a browsing context is created, **Then** an audit event records the key, its kind, the workspace and the establishing agent.
- **AC2: Given** an agent's first action in a context it did not establish, **Then** an audit event records it.

---

## 8. BDD scenarios

**Scenario: handover across an agent switch (Happy Path) — US-1/AC1, FR-001, FR-002, FR-006**
- **Given** workspace W's browsing context has one tab on `https://example.com/a`
- **And** agents Mia and Jim are both on W's CoreTeam, and Jim has `browser_list_tabs: allow`
- **When** Jim calls `browser_list_tabs` in a chat stamped `workspace_id=W`
- **Then** the result is `state:"open"` with one tab whose url is `https://example.com/a`
- **And** no handover tool was called

**Scenario: human browses first, then delegates (Happy Path) — US-2/AC1, FR-016, FR-017**
- **Given** no agent has used the browser in workspace W
- **When** the operator attaches the live panel with `agent_id=mia`, `session_id=S` where S's meta carries `workspace_id=W`, and navigates to `https://example.com/a`
- **And** Jim is then asked, in a chat also stamped `workspace_id=W`, to list tabs
- **Then** Jim sees `https://example.com/a`

**Scenario: cross-workspace isolation survives the re-key (the ADR-043 guarantee) — US-3/AC1, FR-003, FR-004**
- **Given** a login cookie was set on `example.com` in workspace X
- **When** `example.com` is opened from workspace Y
- **Then** the document reports no session cookie
- **And** `browserCtxID(X) != browserCtxID(Y)`

**Scenario: new chat, same workspace, still logged in — US-4/AC1, FR-005**
- **Given** a login cookie set on `example.com` in chat C1 of workspace W
- **When** a new chat C2 in W opens `example.com`
- **Then** the session cookie is present
- **And** `browserCtxID(C1) == browserCtxID(C2)`

**Scenario: unattended delegated sub-turn starts signed out — US-5/AC1+AC2, FR-009, FR-011, FR-012**
- **Given** workspace W's context holds a login on `example.com`
- **And** a delegated sub-turn is running under `spawnSubTurn` with `Viewers(W)==0`
- **When** the sub-turn navigates to `example.com`
- **Then** its browsing key is `un:<its own transcriptSessionID>`, its `browserCtxID` differs from W's, and no session cookie is present
- **And when** the page presents a login wall, **then** the tool result text contains the reason it is signed out

**Scenario: attended delegated sub-turn is NOT isolated (the boundary case) — US-5/AC3, FR-010**
- **Given** the same workspace W and a viewer attached (`Viewers(W)==1`)
- **When** a delegated sub-turn navigates to `example.com`
- **Then** its browsing key is `ws:W`, and the session cookie IS present

**Scenario: scheduled turn resolves to its re-rooted workspace — US-6/AC1, FR-007**
- **Given** a heartbeat turn for agent `ray`, member of workspace W's CoreTeam, with `ToolWorkspaceID(ctx) == ""`
- **When** it calls `browser_navigate`
- **Then** `ResolveBrowsingKey` returns `ws:W`, the same id `workspace.FindForAgentPreferring(home,"ray","")` returns
- **And** the key is never `"default"` and never the agent id

**Scenario: a genuinely workspace-less agent is refused, not merged (Error) — US-6/AC2, FR-008**
- **Given** agents `solo-a` and `solo-b`, neither on any workspace CoreTeam
- **When** each calls `browser_navigate`
- **Then** each result is an error whose text is `ErrNoBrowsingContext`'s
- **And** no browsing context was created for either, and `contextCount()` is unchanged

**Scenario: three tab states are distinguishable (Edge Case) — US-7, FR-013**
- **Given** a resolved key with no browsing context, **When** `browser_list_tabs` runs, **Then** the payload has `state:"no_context"` and an empty `tabs` array
- **And given** a context with two tabs, **Then** `state:"open"` with two entries
- **And given** a context whose tab set is momentarily empty (post-`CloseTab`, pre-`createFirstTab`), **Then** `state:"empty"` with an empty array
- **And** the three payloads are pairwise unequal

**Scenario: a denied agent is told it is denied (Error) — US-8, FR-014**
- **Given** agent `mia`, whose seed (`pkg/coreagent/core.go:848`) grants no `browser_*` entry
- **When** she attempts `browser_list_tabs`
- **Then** the model receives the policy-denial message and no tab payload
- **And** `ListTabsTool.Execute` was never entered

**Scenario: two agents write concurrently; the loser defers, nobody errors — US-9/AC1, FR-019, FR-020, FR-021**
- **Given** agents Jim and Ray on workspace W, both with `browser_navigate: allow`
- **When** both call `browser_navigate` against W's context within the same millisecond
- **Then** exactly one navigation is observed by Chrome
- **And** the other returns `IsError=false` with a body parsing to `{"deferred": true, "reason": <non-empty>}`
- **And** neither result is a Go error

**Scenario: human control outranks the lease — US-9/AC2, FR-022**
- **Given** a human viewer holds the control lock on W's context
- **When** an agent calls `browser_click`
- **Then** the deferral reason is ADR-038 D6's human-control text, and the lease was never acquired

**Scenario: a panicking action tool does not wedge the context — US-9/AC3, FR-024**
- **Given** an action tool acquires the lease and then panics (or its ctx is cancelled)
- **When** another action tool runs on the same context
- **Then** it acquires the lease within `leaseWaitTimeout` and completes normally

**Scenario: read-only tools are never deferred — US-9/AC4, FR-021**
- **Given** Jim holds the write lease on W's context for a long navigation
- **When** Ray calls `browser_screenshot`, `browser_get_text` and `browser_wait`
- **Then** all three execute; none returns a `deferred` body

**Scenario: multi-workspace agent binds to the chat's workspace — US-11/AC1, FR-018**
- **Given** agent `ray` on the CoreTeams of workspaces A and B (A sorts before B)
- **When** the operator chats in session S stamped `workspace_id=B` and attaches the panel with `agent_id=ray, session_id=S`
- **Then** both the turn's browser tools and the panel resolve to `ws:B`
- **And** `FindForAgent`'s sorted-first pick (A) is not used

**Scenario: no wire-schema change holds — US-10/AC1, FR-029**
- **Given** the implementation is complete
- **When** `make gen-contracts` then `make verify-contracts` run
- **Then** exit 0, and the `contracts/` diff touches only `description:` text in `BrowserAttachFrame.yaml`, `BrowserWebRTCOfferFrame.yaml` and `BrowserInspectRequest.yaml`

**Scenario: reload preserves the workspace login — FR-028 (ADR-043 CRIT-002, re-keyed)**
- **Given** workspace W's context holds a login and `coordinator.PID()==P`
- **When** `ReloadProviderAndConfig` runs
- **Then** `coordinator.PID()==P`, W's `browserCtxID` is unchanged, and the login persists
- **And** no manager path called `WithNewBrowserContext`

**Scenario: disposal on workspace deletion — US-12/AC3, FR-026**
- **Given** workspace W has a live browsing context
- **When** W is deleted and the reload prune runs
- **Then** `disposeBrowserContextRaw` is called for W's context exactly once and `contextCount()` drops by one

**Scenario: audit answers "who acted as the signed-in user" — US-13, FR-027**
- **Given** Mia establishes W's browsing context and Jim later acts in it
- **Then** one audit event records the creation (key, kind, workspace, agent=mia)
- **And** one records Jim's first use of a context he did not establish

---

## 9. Traceability matrix (FR ↔ US ↔ BDD ↔ test ↔ ADR/review)

| FR | Requirement | US | BDD | Test (TDD) | ADR / review |
|---|---|---|---|---|---|
| FR-001 | One `BrowserManager` per browsing key; `browserMgrs` re-keyed | US-1 | handover | `TestLoop_BrowserManagerForKey_OnePerKey` | D1.1(1) |
| FR-002 | Tools address the resolved key, not `DefaultSessionID` | US-1 | handover | `TestTools_UseResolvedKeyNotConstant` | D1.1(2) |
| FR-003 | CDP browser-context isolation preserved, re-keyed to workspace | US-3 | cross-workspace-isolation | `TestBrowsingContext_CrossWorkspaceIsolation` | D1.0 / ADR-043 D2 |
| FR-004 | Login in X invisible in Y | US-3 | cross-workspace-isolation | same | ADR crit 5b |
| FR-005 | New chat in same workspace stays logged in | US-4 | new-chat-same-workspace | `TestBrowsingContext_NewChatSameWorkspaceSameCtx` | ADR crit 5c |
| FR-006 | Agent switch requires no handover step | US-1 | handover | `TestHandover_NoCommandRequired` | ADR crit 2 |
| FR-007 | Resolution ladder: workspace ctx → `FindForAgentPreferring` → fail | US-6 | scheduled-turn-resolves | `TestResolveBrowsingKey_Ladder` | D1.4 / C3 |
| FR-008 | No-workspace ⇒ `ErrNoBrowsingContext`, never a shared jar | US-6 | workspace-less-refused | `TestResolveBrowsingKey_NoWorkspaceFailsByName` | D1.4 / C3 |
| FR-009 | Unattended sub-turn keyed by transcript session id | US-5 | unattended-signed-out | `TestResolveBrowsingKey_UnattendedSubTurn` | D1.2 / C2 |
| FR-010 | "Unattended" = sub-turn **AND** `Viewers()==0` | US-5 | attended-subturn-not-isolated | `TestResolveBrowsingKey_AttendedSubTurnUsesWorkspace` | D1.2 |
| FR-011 | The unattended jar is a separate **CDP browser context** | US-5 | unattended-signed-out | `TestUnattended_HasOwnBrowserContextID` | D1.2 (§12 A1) |
| FR-012 | Login-wall failure names the unattended reason | US-5 | unattended-signed-out | `TestUnattended_LoginWallErrorNamesReason` | D1.2 |
| FR-013 | `ListTabsState` returns a closed 3-value state | US-7 | three-tab-states | `TestListTabsState_ThreeDistinctStates` | D1.5 |
| FR-014 | A denied agent's answer is a policy denial, never a tab payload | US-8 | denied-agent | `TestListTabs_DeniedAgentNeverReachesTool` | D1.5 / M5 |
| FR-015 | The 5 model-visible "shared browser session" strings are corrected | US-1 | — | `TestToolDescriptions_NoFalseSharedClaim` | D1.3 / m5 |
| FR-016 | Gateway resolves agent→workspace server-side; no wire field added | US-10 | no-wire-change | `TestGateway_ResolvesManagerWithoutWireChange` | ADR-043 D3 / C4 |
| FR-017 | Gateway prefers the attaching session's `workspace_id` | US-2, US-11 | human-browses-first | `TestGateway_PrefersSessionWorkspaceID` | C4 / §6 Q1 |
| FR-018 | Multi-workspace agent: turn and panel agree | US-11 | multi-workspace-binding | `TestMultiWorkspaceAgent_TurnAndPanelAgree` | §6 Q2 |
| FR-019 | Per-context write lease held for one action tool call | US-9 | two-writers | `TestWriteLease_OneWriterPerContext` | D2.10 / M3 |
| FR-020 | Loser gets non-error `{"deferred":true,"reason":…}` | US-9 | two-writers | `TestWriteLease_LoserGetsDeferredNotError` | D2.10 |
| FR-021 | Read-only tools ungated | US-9 | read-only-never-deferred | `TestWriteLease_ReadOnlyToolsUngated` | D2.10 |
| FR-022 | `controlledResult` evaluated before the lease | US-9 | human-outranks-lease | `TestWriteLease_HumanControlTakesPrecedence` | ADR-038 D6 |
| FR-023 | Bounded wait before declaring contention | US-9 | two-writers | `TestWriteLease_BoundedWait` | §6 (fairness, open) |
| FR-024 | Lease always released on panic/cancel/timeout | US-9 | panic-does-not-wedge | `TestWriteLease_ReleasedOnPanicAndCancel` | D2.10 |
| FR-025 | Reaping semantics asserted, not rewritten | US-12 | — | `TestReap_WorkspaceContext_ViewerPinAndPerTabTTL` | §4 / M4 |
| FR-026 | Disposal on workspace deletion / roster removal | US-12 | disposal-on-workspace-deletion | `TestDispose_OnWorkspaceDeletion` | §6 Q4 |
| FR-027 | Audit on context creation and first cross-agent use | US-13 | audit-repudiation | `TestAudit_ContextCreateAndFirstCrossAgentUse` | D2.11 / M7 |
| FR-028 | Reload preserves pid + context + login (re-keyed) | US-12 | reload-preserves-login | `TestReload_PreservesWorkspaceContextAndLogin` | ADR-043 CRIT-002 |
| FR-029 | `make verify-contracts` green; prose-only schema diff | US-10 | no-wire-change | `make verify-contracts` | Hard Constraint #8 |
| FR-030 | No new platform-conditional behaviour | — | — | `TestLease_IsInProcessOnly_NoFlock` | §6 platform |

---

## 10. TDD plan (ordered; Unit → Integration → E2E)

| # | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 1 | `TestResolveBrowsingKey_Ladder` | Unit | FR-007 | Table-driven over all four ladder rungs. **Write first** — every other stream depends on this contract |
| 2 | `TestResolveBrowsingKey_NoWorkspaceFailsByName` | Unit | FR-008 | Asserts `errors.Is(err, ErrNoBrowsingContext)` **and** that the returned key `IsZero()`. The regression guard against review C3 |
| 3 | `TestResolveBrowsingKey_UnattendedSubTurn` | Unit | FR-009, FR-010 | Fake `ViewerCounter` returning 0; asserts `Kind()==BrowsingKeyUnattended` and the key contains the transcript id |
| 4 | `TestResolveBrowsingKey_AttendedSubTurnUsesWorkspace` | Unit | FR-010 | Same ctx, `ViewerCounter` returns 1 → `ws:W`. **The boundary that makes FR-010 non-vacuous** |
| 5 | `TestListTabsState_ThreeDistinctStates` | Unit | FR-013 | Constructs all three manager states directly; asserts pairwise-distinct payloads and that the state set is exactly `{no_context, open, empty}` |
| 6 | `TestToolDescriptions_NoFalseSharedClaim` | Unit | FR-015 | Greps the five model-visible strings; fails if "shared browser session" survives in a `Description()`/parameter description |
| 7 | `TestWriteLease_OneWriterPerContext` | Unit | FR-019 | Two goroutines, fake action; asserts non-overlapping critical sections |
| 8 | `TestWriteLease_LoserGetsDeferredNotError` | Unit | FR-020 | Asserts `IsError==false` and the body parses to `deferred:true` with a non-empty reason |
| 9 | `TestWriteLease_ReadOnlyToolsUngated` | Unit | FR-021 | screenshot/get_text/wait run while the lease is held |
| 10 | `TestWriteLease_HumanControlTakesPrecedence` | Unit | FR-022 | Pins the composition order; asserts the lease was never acquired |
| 11 | `TestWriteLease_ReleasedOnPanicAndCancel` | Unit | FR-024 | Two sub-cases; the anti-wedge guard |
| 12 | `TestWriteLease_BoundedWait` | Unit | FR-023 | Fake clock; a lease held < timeout is waited out, not deferred |
| 13 | `TestLease_IsInProcessOnly_NoFlock` | Unit | FR-030 | Structural: `lease.go` imports no `fileutil`/`unix` locking |
| 14 | `TestLoop_BrowserManagerForKey_OnePerKey` | Unit | FR-001 | Concurrent callers for one key get one manager; different keys get different managers |
| 15 | `TestTools_UseResolvedKeyNotConstant` | Unit | FR-002 | Structural + behavioural: no non-test reference to `defaultSessionID` survives in the tool call path |
| 16 | `TestListTabs_DeniedAgentNeverReachesTool` | Unit | FR-014 | Policy-filtered registry; asserts the denial message and that `Execute` was not entered |
| 17 | `TestGateway_PrefersSessionWorkspaceID` | Unit | FR-017 | Session meta with `workspace_id=B` beats `FindForAgent`'s A |
| 18 | `TestMultiWorkspaceAgent_TurnAndPanelAgree` | Unit | FR-018 | Two workspaces, one agent; both paths resolve identically |
| 19 | `TestBrowsingContext_CrossWorkspaceIsolation` | Integration (real Chrome) | FR-003, FR-004 | **The amended ADR-043 D2 guarantee.** Modelled on `d2_spike_test.go`; sets a cookie in X, asserts absent in Y, asserts distinct `BrowserContextID` |
| 20 | `TestBrowsingContext_NewChatSameWorkspaceSameCtx` | Integration | FR-005 | The property the workspace axis buys over the conversation axis |
| 21 | `TestUnattended_HasOwnBrowserContextID` | Integration | FR-011 | **Asserts a distinct `cdp.BrowserContextID`, not merely a distinct sessions-map key.** The test that catches §12 A1 |
| 22 | `TestUnattended_LoginWallErrorNamesReason` | Integration | FR-012 | Substring assertions on the reason text |
| 23 | `TestHandover_NoCommandRequired` | Integration | FR-006 | Two agents, one workspace, one tab |
| 24 | `TestReload_PreservesWorkspaceContextAndLogin` | Integration | FR-028 | The re-keyed ADR-043 CRIT-002 regression |
| 25 | `TestReap_WorkspaceContext_ViewerPinAndPerTabTTL` | Integration | FR-025 | Asserts existing semantics survive the re-key; viewer-pin now pins a workspace |
| 26 | `TestDispose_OnWorkspaceDeletion` | Integration | FR-026 | Counts `disposeBrowserContextRaw` calls |
| 27 | `TestAudit_ContextCreateAndFirstCrossAgentUse` | Integration | FR-027 | Two events, correct fields |
| 28 | `TestGateway_ResolvesManagerWithoutWireChange` | Integration | FR-016 | All three gateway surfaces |
| 29 | `TestWriteLease_TwoAgentsRealChrome` | E2E | FR-019, FR-020 | Two agents, real navigations, no interleaved DOM |
| 30 | `verify-contracts` | Build | FR-029 | `make verify-contracts` exit 0 |

### Regression requirements (MANDATORY — this change modifies shipped behaviour)

**Must keep passing, unmodified:**
- `pkg/tools/browser/d2_spike_test.go` — `TestD2Spike_BrowserContextIsolation`. It proves raw CDP context isolation and the `window.open`-stays-in-opener's-context property. **Neither property is keyed by agent**, so this test is axis-agnostic and must pass byte-identical. If it needs editing, the isolation primitive was changed, which D1 forbids.
- `pkg/tools/browser/coordinator_test.go` — `TestManager_Shutdown_DropsConnectionNotProcess` (`:203`), `TestCoordinator_Shutdown_IsSoleKill` (`:244`), `TestCoordinator_OwnershipMarker_RoundTrip` (`:379`), `TestCoordinator_UnlimitedDefault_AllowsPastOldCap` (`:429`), `TestCoordinator_PositiveCap_StillRejectsAtBoundary` (`:448`), `TestCoordinator_ConcurrentOpeners_PositiveCap_ExactlyOneWinner` (`:472`), `TestCoordinator_SetMaxTotalTabs_ReloadRestoresUnlimited` (`:506`).
- `pkg/tools/browser/tab_adoption_e2e_test.go` — all nine ADR-041 tab tests (`:77`–`:569`). Tab-set semantics inside one context are untouched.
- `pkg/tools/browser/shared_control_test.go` — all nine (`:35`–`:186`). ADR-038 D6 human-control behaviour must be **unchanged** by the lease; FR-022 exists precisely so these stay green.
- `pkg/tools/browser/idle_reaper_test.go`, `reaper_edge_test.go`, `reaper_lifecycle_test.go` — FR-025 asserts the reaper is not rewritten.

**Must be rewritten, not extended (they encode the per-agent model — review §4):**
- `pkg/tools/browser/coordinator_test.go:154` `TestCoordinator_TwoAgents_OneChrome_TwoContexts` → becomes `TestCoordinator_TwoWorkspaces_OneChrome_TwoContexts`. **Its per-agent assertion is now the wrong assertion**, and leaving it green while the model changed underneath is exactly the `docs/internal/false-green-patterns.md` stale-green shape.
- `pkg/tools/browser/coordinator_test.go:328` `TestCoordinator_Register_SharedContextMode_ReturnsRootCtxAndEmptyBrowserCtxID` → re-key the Register argument.
- `pkg/tools/browser/stress_5agents_test.go:267` `TestFiveAgents_ConcurrentStress` → must become **five agents on one workspace** (contention, the new normal case) **plus** five agents across five workspaces (isolation). Five agents on five implicit per-agent jars is no longer a scenario the product has.

**Verification receipt discipline:** run each scoped test as `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/tools/browser/ > log 2>&1; echo "exit=$?"` — never through a pipe (a piped `tail` reports tail's status). Do not run the full suite locally; push and read CI.

### Test datasets

| Input | Expected | Traces to |
|---|---|---|
| ctx: `workspace_id=W`, not a sub-turn | key `ws:W` | FR-007 |
| ctx: `workspace_id=""`, agent on W's CoreTeam | key `ws:W` (via `FindForAgentPreferring`) | FR-007 |
| ctx: `workspace_id=""`, agent on **no** CoreTeam | `ErrNoBrowsingContext`, zero key | FR-008 |
| ctx: sub-turn, `workspace_id=W` (inherited, `subturn.go:1323`), `Viewers(ws:W)==0` | key `un:<transcriptID>` | FR-009, FR-010 |
| ctx: sub-turn, `workspace_id=W`, `Viewers(ws:W)==1` | key `ws:W` | FR-010 |
| ctx: sub-turn, `workspace_id=""`, agent on no CoreTeam, `Viewers==0` | key `un:<transcriptID>` (a sub-turn always has a transcript id) | FR-009 |
| ctx: sub-turn with empty transcript session id | `ErrNoBrowsingContext` — never an empty-suffixed key | FR-008 |
| agent on workspaces A and B; session meta `workspace_id=B` | `ws:B` from **both** the turn and the gateway | FR-018 |
| agent on workspaces A and B; session meta empty | `ws:A` (documented sorted-first tie-break) from **both** | FR-018 |
| manager with no `sessions` entry for its key | `TabStateNoContext`, empty tabs | FR-013 |
| manager, context live, 2 tabs | `TabStateOpen`, 2 tabs | FR-013 |
| manager, context live, `len(se.tabs)==0` | `TabStateEmpty`, empty tabs | FR-013 |
| `mia` calls `browser_list_tabs` | policy denial; `Execute` not entered | FR-014 |
| 2 concurrent `browser_navigate` on one key | 1 executes, 1 `deferred:true`, 0 errors | FR-019, FR-020 |
| 8 concurrent action tools on one key | 1 executes at a time; 7 deferred or waited; 0 errors; 0 deadlocks | FR-019, FR-023 |
| lease holder panics mid-action | next acquire succeeds ≤ `leaseWaitTimeout` | FR-024 |
| human holds control lock + agent action | ADR-038 D6 reason; lease never acquired | FR-022 |
| `browser_screenshot` while lease held | executes normally | FR-021 |
| workspace W deleted with a live context | exactly one `disposeBrowserContextRaw(W)` | FR-026 |
| reload mid-browse with a login in W | pid unchanged, ctx id unchanged, cookie present | FR-028 |

---

## 11. Functional requirements & success criteria

- **FR-001 … FR-030** as enumerated in §9. All MUST.
- **SC-001 (headline, the reported defect):** the ADR §1.1 conversation cannot recur. Browse as Mia in workspace W; switch the chat to Jim; Jim's `browser_list_tabs` returns the tab. Measured as scenario "handover across an agent switch" passing against real Chrome.
- **SC-002 (isolation did not vanish):** `TestBrowsingContext_CrossWorkspaceIsolation` passes against real Chrome with **distinct `cdp.BrowserContextID`s** asserted, alongside an unmodified `TestD2Spike_BrowserContextIsolation`.
- **SC-003 (no silent merge):** across the full test run, zero browsing contexts are created with a key that did not come from `ResolveBrowsingKey`. Asserted structurally: `BrowsingKey`'s field is unexported and `key.go` exposes no other constructor.
- **SC-004 (unattended is genuinely signed out):** `TestUnattended_HasOwnBrowserContextID` asserts a distinct `BrowserContextID`, not merely a distinct map key.
- **SC-005 (concurrency is deterministic):** 8 concurrent action tools on one workspace context, repeated 50× under `-race`, produce zero errors, zero deadlocks and exactly one executing writer at any instant.
- **SC-006 (three states):** a table-driven test enumerates all three `ListTabsState` values and asserts pairwise-distinct model-visible payloads; a fourth value is a compile-time impossibility (closed enum) and a new value fails `TestListTabsState_ThreeDistinctStates`.
- **SC-007 (contract intact):** `make verify-contracts` exits 0; the `contracts/` diff contains no `properties:`, `required:`, `enum:` or `type:` change.
- **SC-008 (nothing green by accident):** every rewritten test in §10's regression list is confirmed to **fail** against the pre-change code and **pass** after — the mutation check `docs/internal/false-green-patterns.md` requires. A rewritten test that passes both ways is not evidence.
- **SC-009 (gates):** `gofmt -l . | wc -l` = 0; `golangci-lint run --build-tags=goolm,stdjson` exit 0; CI `go test -tags goolm,stdjson -count=1 ./...` exit 0; `govulncheck` 0; `npm run typecheck` exit 0.

---

## 12. Ambiguity self-audit

Ten places where the ADR is underspecified or where its own text does not survive contact with the code. Each is **resolved here as an assumption**; a different ruling changes the spec.

| # | Ambiguity | Resolution (recorded assumption) |
|---|---|---|
| **A1** | **D1.2 implies a key change is sufficient to make an unattended sub-turn signed out. It is not.** `BrowserManager.browserCtxID` is a **single** field (`manager.go:381`) applied to every session it bootstraps (`manager.go:1369`). A second entry in `m.sessions` under a transcript key would reuse the **same CDP browser context** — the sub-turn would be fully signed in and ADR criterion 17 would fail while every obvious test passed. | **DECIDED (FR-011):** the unattended key gets its own coordinator-owned CDP browser context via `Coordinator.Register`, and the manager's single `browserCtxID` becomes a per-key map with invariants M-1/M-2. This is a structural change the ADR does not name. **The highest-value single item in this spec.** |
| **A2** | **D1.3 claims "both keys already exist and already reach every tool" and "no new identity concept is introduced". The keys do; the discriminator does not.** There is no context value meaning "running under `spawnSubTurn`". `ToolDelegationDepth` (`base.go:292`) exists but is set only by `task_executor.go:874,2693` and is 0 for a `delegate`-spawned sub-turn. Worse, `spawnSubTurn` **inherits** the parent's workspace (`subturn.go:1323`), so without a discriminator an unattended child lands in the parent's jar by default. | **DECIDED (Stream B, FR-010):** add `tools.WithSubTurn`/`ToolSubTurn`, mirroring the `WithDelegationDepth` pair, set in `spawnSubTurn` and injected alongside `loop.go:7968-7988`. Reusing `ToolDelegationDepth` was rejected: overloading a task-generation counter as a delegation-provenance flag would make both meanings wrong for someone. |
| **A3** | **D1.2 defines "unattended" as "no viewer attached to the workspace's live panel" but there is no way to ask.** `se.viewers` is incremented/decremented at `manager.go:2818,2836`; `LiveViewRegistry` exposes `Controller`/`IsControlled` but **no viewer count**. | **DECIDED (FR-010):** add `BrowserManager.Viewers() int` and the `ViewerCounter` seam, so the condition is testable without Chrome. Note the consequence: attendance is evaluated **once, at key resolution**. A viewer who attaches mid-sub-turn does not retroactively move the sub-turn into the workspace jar. Recorded as accepted; the alternative (re-resolving per tool call) would make a sub-turn's cookie jar change under it mid-task. |
| **A4** | **D1.5's premise is partly stale.** It cites `manager.go:1605-1613` returning `nil, 0, nil` for two states — true — but `ListTabsTool.Execute` **already** distinguishes them via `browser_started` (`tabs.go:58,66`, the "B-8 fix"). And the third state, "not permitted", is produced by the policy layer (`tool_denial.go:206-210`) and can never be a return value of a tool that policy stopped from running. | **DECIDED (FR-013/FR-014):** build on the existing flag rather than reinventing it — replace the boolean with the closed three-value `state` enum, and spec the third state as an **end-to-end observable** (the model receives a policy denial, never a tab payload) with its own test, rather than as a `ListTabs` return value. Do not add a "denied" value to `TabState`; a tool that never ran cannot report why. |
| **A5** | **The ADR's three states include one the code says cannot occur and omit one that can.** `tabs.go:50-52` asserts a running browser with zero tabs "cannot occur (ADR-041's own invariant)", yet `ReapIdleSessions` has a real, documented zero-tab branch (`manager.go` — `CloseTab` empties `se.tabs` and a failed `createFirstTab` leaves it empty). Meanwhile D1.4's *resolution failure* — a genuinely new observable state — is not in D1.5's list. | **DECIDED:** `TabStateEmpty` is specced as **reachable but transient** (FR-013/AC3). Resolution failure is **not** a `TabState` — it is an `ErrNoBrowsingContext` tool error (FR-008), because an error is the only shape a model reliably treats as "stop and report" rather than "nothing was open". |
| **A6** | **"No wire change needed" (D1.0 / ADR-043 D3 vs review C4).** Verified: `agent_id` remains the binding key in `BrowserAttachFrame.yaml`, `BrowserWebRTCOfferFrame.yaml` and `BrowserInspectRequest.yaml`; `session_id` is already carried and is already declared correlation-only; the gateway can resolve `agent_id + session_id → workspace_id` server-side from `session.UnifiedMeta.WorkspaceID` (`unified_meta_files.go:60`). **The claim holds** — but only because the session id is already on every frame. | **DECIDED (FR-016/FR-017):** no field is added. Descriptions change (which still requires `make gen-contracts` + a committed generated diff). **Loud flag for the implementer:** if resolution turns out to need a workspace id the frames do not already carry, **stop and amend this spec** — Hard Constraint #8 makes that the 5-step contract-first process (`contracts/components/schemas/` → `openapi/asyncapi` → `scripts/gen-contracts.sh` → one atomic commit → generated types only), not a code change. |
| **A7** | **The write lease is filed under D2.10 but ADR §4 calls it "the largest open risk in D1".** Two specs could claim it or neither could. | **DECIDED:** this spec owns it (FR-019…FR-024, Stream D). The D2 spec must reference these FRs, not restate them. If the D2 spec also specs a lease, one of the two must be deleted before implementation starts. |
| **A8** | **Fairness under sustained contention is an explicit ADR open question.** A per-action, first-come lease guarantees no progress for either of two steadily-browsing agents. | **ASSUMED:** bounded wait (`leaseWaitTimeout`, **assume 2s**, tunable) then defer, with no queue and no fairness guarantee — matching the ADR's stated scope. FR-023 makes the bound testable so "unfair" is at least *bounded* rather than unbounded. A starvation-free queue is deferred, not forgotten. |
| **A9** | **Upgrade path for existing per-agent state is unspecified** (review §6 Q3). Per-agent contexts and any persisted profile data exist on installs today. | **DECIDED:** **discard, do not merge.** Merging per-agent jars into a workspace jar would pool logins from agents that never shared them — a silent privilege grant at upgrade time, which is precisely the elevation-of-privilege risk D2.11 names. Operators re-log-in once. This must appear in release notes. |
| **A10** | **Who is the operator of record for the 2026-08-31 rulings** (review §6 Q8). Six decisions in D1 rest on "operator ruling, 2026-08-31" with no named decider, and D1 overrides a named ADR-043 limitation on that authority alone. | **UNRESOLVED — flagged, not assumed.** The spec implements the rulings as written. If the attribution matters for the ADR's acceptance, it must be added to ADR-072 before this spec is implemented; it is not something a spec can decide. |

**Additional corrections folded in silently above, listed here so a reviewer can check them:** the ADR's `pkg/agent/loop.go:185` is actually `:279`; "six tool descriptions" is actually 5 model-visible strings + 2 comments + 1 unrelated SPA comment; `pkg/tools/base.go:241-251` is `:243-252` for the workspace pair (`WithWorkspaceID` at `:243`, `ToolWorkspaceID` at `:250`); `pkg/tools/resolvepath.go:695-709` is prose whose actual call is `:713`.

---

## 13. Holdout evaluation scenarios (post-implementation; NOT in the TDD plan or traceability)

1. **(happy)** Operator opens the live panel in a workspace chat with Mia, logs into a real site, then switches the chat to Jim and asks "what's open?" — Jim names the page and can act on it. The verbatim ADR §1.1 conversation, re-run.
2. **(happy)** Operator opens a *new* chat in the same workspace the next day and asks Ray to check the same site — still logged in, no re-auth.
3. **(edge)** Operator has two workspaces for two clients, logged into the same SaaS with different accounts. Each workspace's agents see only their own account. Nothing in either UI hints at the other.
4. **(error)** Operator asks Mia (policy-denied) what is open. She says she is not permitted to see the browser — she does not say the browser is empty, and she does not claim it is shared.
5. **(error)** A scheduled heartbeat for an agent on no workspace at all runs a browser step — it fails with the named error, the log shows the refusal, and no browsing context was created.
6. **(edge)** Operator asks Jim to delegate a long research task to Ray, then closes the browser panel. Ray's sub-turn hits a login wall and reports "this ran unattended and has no signed-in session" — the operator re-asks in a normal chat and it works.
7. **(edge)** The same delegation, but the operator leaves the panel **open**. Ray's sub-turn browses the signed-in workspace session and completes.
8. **(edge)** Two agents on one workspace are told to browse different sites simultaneously. Both complete; neither reports an error; the transcript shows at most one deferral apiece and no interleaved page state.
9. **(edge)** Operator saves an unrelated Setting mid-browse. Chrome's pid is unchanged, the login survives, and the panel keeps streaming.
10. **(edge)** Operator deletes a workspace whose browser had tabs open. The tabs close, Chrome survives, other workspaces are unaffected.
11. **(edge)** An agent is added to a second workspace mid-session. Its next turn in the original chat still resolves to the original workspace (the chat's `workspace_id` wins the tie-break) — no silent jar swap.

---

**Next:** grill-spec this document, reconcile with the D2 spec on ambiguity A7 (lease ownership), then implement Stream A's interface first and fan out B–F.
