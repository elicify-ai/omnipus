# Spec — Browser ownership: workspace-scoped browsers (ADR-072 **D1**)

- **Source ADR:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions.md` — **D1 only (D1.0, D1.0a, D1.1, D1.1a, D1.2, D1.3–D1.5)**, plus the write lease the ADR files under D2.10 but §4 attributes to D1 (see §14).
- **Round-1 ADR review folded in:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions-review.md`.
- **Round-2 spec review folded in:** `docs/internal/specs/browser-workspace-ownership-spec-review.md` — verdict **BLOCK**, 29 findings. Every one is dispositioned in **§15**; three are rejected with evidence and say so there.
- **Amends:** **ADR-043 D1** (one shared Chrome for the process — *this spec replaces it with a pool*), **ADR-043 D2** (per-agent CDP browser context — *replaced by per-workspace Chrome profiles*) and **ADR-043 D3** (live-view binding). Read ADR-043 first; D1 has the largest blast radius of anything in ADR-072.
- **Sibling spec:** D2 (capability). **This spec owns the write lease — §14 is its single normative definition.** The D2 spec must delete its own lease FR/US/stream/test and reference §14 (operator ruling, 2026-08-31).
- **Worktree:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf` · **Branch:** `feat/browser-streaming-performance`
- **Status:** Draft for implementation, **gated on two measurements** (§0.3) — not on a ruling. All design questions are decided.
- **Operator rulings folded in (2026-08-31, Daniel Piatkowski):** workspace is the isolation axis, not the agent and not the conversation; **isolation is by Chrome process + profile directory, not by CDP browser context** (D1.1a); **every agent on a workspace shares its browser and its logins, including unattended delegated work** (D1.2, superseding the earlier same-day ruling); every turn runs in a workspace (no workspace-less fallback); the browser seed stays Jim + Ray only; the write lease belongs to this spec.

**Citation policy.** `pkg/agent/loop.go`, `turn.go` and `subturn.go` are ~11k-line files under constant churn; per the root `CLAUDE.md` this spec cites them as `file::symbol`. Line numbers appear only where the file is stable or where the exact line *is* the evidence (a config seed, a literal string, a hardcoded constant). Every `file:line` below was re-verified on this worktree on 2026-08-31.

---

## 0. What changed since the previous draft, and what still gates it

### 0.1 The mechanism changed: process isolation, not context isolation

The previous draft was built on "the isolation primitive is unchanged — one CDP browser context per unit, only the key changes." **That premise was false, and the correction changed the design rather than the wording.**

| Fact | Evidence |
|---|---|
| The shipping default creates **no** per-unit browser context at all | `pkg/config/defaults.go:671` seeds `CaptureSharedContext: true`; in that mode `BrowserCoordinator.Register` returns an **empty** `browserCtxID` (`pkg/tools/browser/coordinator.go:349-359`) and logs *"per-agent browser-context isolation is OFF"* |
| An empty id means one cookie partition process-wide | `manager.go:1360-1370` — `bootstrapBrowserCtx` omits `chromedp.WithExistingBrowserContext` when the id is empty |
| The cause is a hard Chrome constraint, not a preference | `coordinator.go:330-348` — `chrome.tabCapture` *"hard-fails with `Invalid tab specified.` for ANY tab living in a CDP-created browser context … verified against real Chrome 150"*. **A CDP-isolated tab cannot be video-streamed at all.** |
| The documented escape hatch is gone | `pkg/config/config.go:3800-3849` still tells operators the *"JPEG `browser_screencast` fallback keeps working either way"*. ADR-061 deleted that path and `scripts/check-no-jpeg-screencast.sh` prevents its return |

So CDP browser contexts cannot deliver isolation **and** a live panel. **The decision (ADR D1.1a) is to isolate by Chrome process and `--user-data-dir` profile directory instead — one Chrome per workspace.** Cookies are isolated because the profiles are; the panel still captures because each Chrome carries its own extension; and, unlike a CDP context, the profile is on disk and therefore survives a reload.

**Consequence for this spec: it now specifies a browser-process pool** — the escape hatch ADR-043 deliberately did not build. That is §3's Stream A and FR-037…FR-045, and it is the largest single piece of work here.

**One prior blocker, checked and dismissed — do not re-raise it.** ADR-043 rejected multiple Chromes partly because the DevTools port was pinned at 9223 and a dynamic per-manager port could not be followed by the compiled Landlock/seccomp allow-list. That reason has evaporated: Chrome is driven over `--remote-debugging-pipe` on inherited fds 3/4 (`pkg/tools/browser/exec_resolver.go:60` — *"There is NO `--remote-debugging-port` — CDP flows over the inherited fd 3/4"*) and the allow-list entry was removed with the port (`pkg/gateway/sandbox_apply.go:405-417`). **N Chromes are N pipes and nothing to allow-list.** (`pkg/tools/browser/manager.go:1330-1345` still describes a pinned port; that comment governs the legacy no-coordinator fallback, not the coordinator path this spec changes. It should be corrected in passing.)

### 0.2 The scope shrank: unattended delegated work is no longer a separate case

ADR D1.2 was rewritten by a superseding ruling: **every agent on a workspace shares that workspace's browser and its logins, including unattended delegated work.** The earlier same-day ruling (background work starts signed out) is reversed, informedly: when a second jar was a CDP context it was nearly free; under D1.1a it is another whole Chrome process per background job.

**This deletes the hardest part of the previous draft.** Not deferred — deleted:

- the attended/unattended discriminator (`tools.WithSubTurn`/`ToolSubTurn`) is **not built**. `spawnSubTurn` inheriting the parent's `WorkspaceID` (`pkg/agent/subturn.go:1323`) is now **correct behaviour**;
- `BrowserManager.browserCtxID` stays a **single field** (`manager.go:381`) — one browser per manager, one manager per workspace. The per-key map the previous draft called its "highest-value single item" is not built;
- `tools.ToolTranscriptSessionID` is **not** a browsing key;
- `BrowsingKey` has exactly **one** shape, `ws:<workspaceID>`. `BrowsingKeyKind` is not built;
- a `ViewerCounter` seam for attendance is not built. (`BrowserManager.Viewers()` is still added — FR-010 — but for the *reaper* and the pool's idle-close decision, not for attendance.)

**The accepted risk, stated once and only once:** an unattended agent can act as the operator on any site its workspace is signed into — a purchase, a post, a message sent by a process nobody is watching. The single remaining gate is `browser_upload_file`'s global `ask` seed (ADR D2.9), and **issue #659 is its prerequisite** (FR-032).

### 0.3 What still gates implementation — two measurements, no open decisions

Neither is a design question. Both are numbers nobody has, and both size or validate the pool.

- **G-1 — per-Chrome memory (FR-044).** ADR-043's "≈4–5 GB at ten agents" is labelled in its own text as a rough, unmeasured order-of-magnitude estimate, and it was per *agent*. Workspaces are far fewer. **Measure the marginal RSS of the Nth Chrome (browser process only, one blank tab, on the UAT box) before setting `max_browsers`' default.** Ship no cap default that is a guess.
- **G-2 — capture against a second Chrome's default context (FR-045).** ADR D1.1a's claim that "each Chrome carries its own extension, so `chrome.tabCapture` works" is the same *class* of claim that proved false for CDP contexts, and its falsification cost a whole design. **Prove it with a spike against real Chrome before the pool is built** — two Chromes, distinct `--user-data-dir`s, extension loaded in each, capture a tab in the second. If it fails, stop and re-open D1.1a; do not build the pool first.

Both are cheap, both are one-off, and neither blocks the §0.4 work.

### 0.4 What is not gated

Everything that is about *ownership* rather than *partitioning* — and this is the part that fixes the reported defect:

- one manager and one tab set per workspace (FR-001, FR-002, FR-002a, FR-002b, FR-002c) — **this alone fixes ADR criteria 2 and 3**, because handover is broken by the *manager* split, not by any partition;
- the resolution ladder, its named failure, and a distinguishable panel failure reason (FR-007, FR-008, FR-008a);
- the reload prune and per-key idempotent registration (FR-026a, FR-026b);
- the three-state tab answer, the description corrections and the denial that names the browser (FR-013, FR-014, FR-014a, FR-015, FR-034);
- the write lease (§14);
- audit, gateway resolution, capture registry, warm path (FR-016, FR-016a, FR-016b, FR-017, FR-018, FR-027).

**Sequence: land §0.4 first, run G-1 and G-2 in parallel, then build the pool.**

---

## 1. Overview / Actors / Scope

**Problem.** The browser — its tab set *and* its logins — is owned by the **agent**, so it strands the moment the operator switches who they are talking to. `AgentLoop.browserMgrs` is `map[agentID]*browser.BrowserManager` (`pkg/agent/loop.go::AgentLoop`), populated by a **per-agent** registration loop (`loop.go::registerSharedTools`) that calls `browser.RegisterTools` and then `mgr.AttachSharedChrome(coordinator, agentID)`. `RegisterTools` (`pkg/tools/browser/register.go:41-84`) constructs a manager and **binds it into eleven tool structs** — `&NavigateTool{mgr: mgr}` at `:65` through `&OpenTabTool{mgr: mgr}` at `:81`. Every tool then addresses its tabs through one hardcoded key, `DefaultSessionID = "default"` (`pkg/tools/browser/tools.go:63`). The operator browses with Mia, switches the chat to Jim, and Jim — correctly, for his own manager — reports zero tabs while telling the operator the browser is "shared across the workspace", because five model-visible strings say exactly that (`tabs.go:32,86,143,206`; `tools.go:415`).

**Solution (ADR-072 D1).** Move ownership from the **agent** to the **workspace**:

1. **One Chrome process and one on-disk profile per workspace** (D1.1a), replacing ADR-043's single process-wide Chrome and its per-agent CDP browser contexts. This is what isolates cookies, and it is what keeps the live panel capturable.
2. **One `BrowserManager` per workspace**, shared by every agent on that workspace's team, resolved **per tool call** from the turn's context — never captured at registration time.
3. **Every agent on the workspace shares it**, delegated sub-turns included (D1.2).

A login obtained in workspace X is invisible in workspace Y because they are different Chrome profiles. A new chat in the same workspace is still logged in. An agent switch changes nothing.

> **Three claims the previous draft made that are now wrong, recorded so a reader of the git history does not resurrect them.**
> 1. *"The isolation primitive is unchanged — only its key changes."* False (§0.1). The primitive changes from CDP browser context to Chrome process + profile.
> 2. *"Every browser tool passes a session id today, so this is a parameter change."* False. The session id is a parameter; the **manager** is a captured struct field (`register.go:65-81`). Re-keying `browserMgrs` alone leaves handover broken while every map-level test passes (FR-002a).
> 3. *"An unattended delegated sub-turn gets its own signed-out jar."* Reversed by ruling (§0.2). It shares the workspace's browser.

**Actors:**

| Actor | Where | Role under D1 |
|---|---|---|
| `AgentLoop` | `pkg/agent/loop.go` | Owns `browserMgrs`, the coordinator/pool, turn context injection (`::runTurn` sets `tools.WithWorkspaceID` **only when `ts.opts.WorkspaceID != ""`**), the per-agent registration loop (`::registerSharedTools`), the reload prune (`loop.go:2849-2871`) |
| `browser.RegisterTools` | `register.go:41-84` | **Constructs the manager and binds it into 11 tools.** Must stop doing the first half (FR-002a) |
| `BrowserCoordinator` | `coordinator.go:114` (struct), `:226` (ctor) | Today: singular. One `rootCtx`, one `rootCancel`, one `cmd`, one `launched/launching/launchDone`, one launch lock, one ownership marker, one `watchForCrash`, `PID() int`. **Becomes a pool** (FR-037) |
| `BrowserManager` | `manager.go` | Owns the connection, `sessions` (`:338`), the single `browserCtxID` (`:381`, now unused — FR-031), viewer counters (`::ViewerAttached`/`::ViewerDetached`), the reaper (`::ReapIdleSessions`) |
| The eleven browser tools | `register.go:65-81` | Each holds `mgr *BrowserManager`; all eleven must resolve per `Execute` |
| Gateway live surfaces | `browser_webrtc.go`, `browser_ws.go:1252`, `browser_inspect.go:73` | All call `BrowserManagerForAgent(agentID)` — one argument today |
| Capture registry | `browser_webrtc.go:70-78` | `sessions map[string]*browser.CaptureSession // keyed by agentID` (FR-016a) |
| Boot warm path | `gateway.go:3373` `pickWarmBrowserManager`, called `:3562` | Selects by `mgr.AgentID()` and `agents.defaults.default_agent_id` (FR-016b) |
| `controlledResult` | `tools.go:962-963` | `mgr.Live().IsControlled(defaultSessionID)` — hardcoded (FR-002c) |
| `workspace.FindForAgentPreferring` | `find_for_agent.go:176` → `FindForAgent` `:83` | The resolution ladder; sorted-first tie-break documented `:45-48` |
| `ensureDefaultWorkspace` | `rest_workspaces.go:468`, called `gateway.go:5013` | Seeds "My Workspace" with the full built-in roster on **every** boot. This is why a fresh install is **not** workspace-less (§15, MAJ-003 rejected) |

**In scope (D1):**

- **The browser-process pool** — one Chrome + profile dir per workspace browsing key, with a cap, a refusal (not an eviction) at the cap, whole-Chrome idle close, per-Chrome crash containment, and per-Chrome launch locks and ownership markers (FR-037…FR-043).
- One `BrowserManager` per workspace, with **per-`Execute` manager resolution** replacing registration-time binding (FR-002a).
- Every `DefaultSessionID` consumer — **37 non-comment references**, enumerated in §2 — re-pointed at the resolved key; the constant deleted (FR-002b), including `controlledResult` (FR-002c).
- Workspace resolution ladder with **no constant fallback**, a named failure, and a distinguishable gateway/panel reason (FR-007, FR-008, FR-008a).
- Reload-prune liveness keyed by browsing key; per-key idempotent registration (FR-026a, FR-026b).
- Three-state `browser_list_tabs` (D1.5), the five model-visible description strings with their **replacement literals specified** (FR-015, FR-034), and a policy denial that names the browser surface (FR-014a).
- The per-workspace **write lease** — §14 is the single normative definition (FR-019…FR-024, FR-019a).
- Gateway server-side agent→workspace resolution (FR-016, FR-017, FR-018), capture registry re-keying (FR-016a), boot warm path (FR-016b).
- Audit on browser creation and first cross-agent use (FR-027), with provenance asserted (FR-035).
- Retirement of `tools.browser.capture_shared_context` and the CDP-context machinery it gated (FR-031).
- The two measurement gates (FR-044, FR-045).

**Out of scope (explicitly):**

- Everything in **D2** — role/accessible-name selection, actionability, `browser_select_option` / `browser_press_key` / `browser_hover` / `browser_upload_file` / dialog handling / `browser_snapshot`, tier assignment (D2.8), policy seeding (D2.9), the D2.11 information-disclosure bullet. **But:** D2's new action tools inherit §14's lease *by rule* (FR-019a), and D2.9's `ask` seed has a prerequisite that D1 makes dangerous (FR-032).
- Mid-tool preemption and sustained-contention fairness beyond §14's bounded wait (ADR §6, open).
- Re-keying the `serve_web` preview URL (ADR §6, open; `/preview/<agent>/<token>/` stays agent-scoped). **The registered tool name is `serve_web`** (`pkg/tools/web_serve.go:46`, `const ToolNameWebServe = "serve_web"`); an earlier revision of this spec wrote `web_serve` twice, which is the *file's* subject, not a tool any agent can call.
- Changing the seeded browser policy roster. Jim (`pkg/coreagent/core.go:1052-1064`) and Ray (`:910-921`) keep it; Mia (`:848`) and Ava (`:794`) stay deny-by-default. Operator-confirmed 2026-08-31.
- Multi-process safety for two gateways on one `$OMNIPUS_HOME` beyond the per-workspace launch lock (§12, A11).
- Migrating existing per-agent browser state (§12, A9 — *discard*; under today's default there is usually nothing to discard).

---

## 2. Existing Codebase Context

### 2.1 Symbols involved

| Symbol | Role | Context (verified 2026-08-31) |
|---|---|---|
| `AgentLoop.browserMgrs` (`loop.go`, field decl ~`:279`) | **modifies** | `map[string]*browser.BrowserManager`, agent-keyed → **browsing-key-keyed**. Its standing comment (`loop.go:270-279`) forbids reverting to a single shared field *"the gateway's live-view WS handler needs a specific agent's manager, not 'whichever agent registered last'"* — that instruction survives the re-key and its replacement text is specified in FR-002d |
| `browser.RegisterTools` (`register.go:41-84`) | **modifies** | Calls `NewBrowserManager` at `:49`, then binds the result into 11 tool structs at `:65-81`. **The single most important omission from the previous draft** (FR-002a) |
| The 11 tool structs (`register.go:65-81`) | **modifies** | `NavigateTool`, `ClickTool`, `TypeTool`, `ScreenshotTool`, `GetTextTool`, `WaitTool`, `EvaluateTool`, `ListTabsTool`, `SwitchTabTool`, `CloseTabTool`, `OpenTabTool` — every one carries `mgr *BrowserManager` |
| `AgentLoop.BrowserManagerForAgent` (`loop.go::BrowserManagerForAgent`) | **modifies** | **One argument today** (`browser_inspect.go:73`, `browser_ws.go:1252`). Gains `preferredWorkspaceID` (FR-017) |
| `AgentLoop.BrowserManagers()` | **reuses** | Snapshot slice for reaping/Close; membership changes |
| reload prune (`loop.go:2849-2871`) | **modifies** | `registeredAgentIDs := registry.ListAgentIDs()` at `:2849`; `coord.RemoveAgent(id)` at `:2866`. **Its membership predicate is agent ids** — a workspace-keyed map matches nothing and every browser is destroyed on every Settings save (FR-026a) |
| registration loop (`loop.go::registerSharedTools`) | **modifies** | Runs `RegisterTools` + `AttachSharedChrome` + `prior`/`Release`/`Shutdown` **once per agent**. N agents on one workspace ⇒ N teardown/replace cycles against one key (FR-026b) |
| `BrowserCoordinator` (`coordinator.go:114-226`) | **modifies (structural)** | Singular by construction: one `rootCtx`/`rootCancel`/`cmd`, one `launched`/`launching`/`launchDone` single-flight, `PID() int` (`:849`), one `watchForCrash` (`:1357`), one launch lock at `cfg.ProfileDir/shared-chrome.lock` (`:1424`), one ownership marker at `$OMNIPUS_HOME/browser/shared-chrome.pid` (`:1527`). **Becomes a pool** (FR-037, FR-042) |
| `BrowserCoordinator.Register` (`:311`) | **modifies** | `(ctx, agentID, mgr) → (rootCtx, browserCtxID, err)`; the `capture_shared_context` short-circuit at `:349-359` and the CDP-context branch below it both **retire** (FR-031) |
| `BrowserCoordinator.RemoveAgent` (`:542`), `disposeBrowserContextRaw` (`:585`), `contextCount()` (`:1111`) | **modifies / retires** | Disposal becomes "close this workspace's Chrome", not "dispose a CDP context" (FR-031, FR-040) |
| `BrowserCoordinator.launchChrome` (`:1212`) / `ensureLaunched` (`:1127`) | **modifies** | Per-key, single-flight per key |
| `BrowserCoordinator.watchForCrash` (`:1357`) | **modifies** | Today resets **every** connector manager and relaunches the one Chrome. Must become per-Chrome (FR-041) |
| `pipeLaunchConfig.userDataDir` (`exec_resolver.go:385` `UserDataDir: cfg.userDataDir`) | **modifies** | The seam D1.1a's isolation rides on: one profile dir per workspace (FR-037) |
| `maxTotalTabs` / `TryOpenTab` (`coordinator.go:782`) | **extends** | A global cross-agent **tab** budget, unlimited by default. Joined by a **browser** cap (FR-038) |
| `BrowserManager.browserCtxID` (`manager.go:381`) | **retires** | Stays a single field and becomes permanently empty — every manager drives its own Chrome's default context (FR-031). **Not a map** (§0.2) |
| `BrowserManager.sessions` (`manager.go:338`) | **modifies** | `map[string]*sessionEntry`; one entry, under the browsing key instead of `"default"` |
| `BrowserManager.AttachSharedChrome` (`manager.go:537`) | **modifies** | Sets `m.agentID` (`:375`) — the coordinator's Register/Release/RemoveAgent key. Becomes the browsing key |
| `BrowserManager.ListTabs` (`manager.go:1605`) | **modifies** | `return nil, 0, nil` on a missing session (`:1609-1611`) — the two-state collapse (FR-013) |
| `BrowserManager.sessionExists` (`manager.go:2378`) | **reuses** | Already backs `browser_started` (`tabs.go:58`) — the existing half of D1.5 |
| `BrowserManager.ViewerAttached` / `ViewerDetached` (`manager.go::ViewerAttached`, `::ViewerDetached`; `se.viewers++` / `--` in their bodies) | **extends** | No exported count accessor exists → FR-010 adds `Viewers()`, used by the **reaper and the pool's idle-close**, not for attendance (§0.2) |
| `BrowserManager.ReapIdleSessions` (`manager.go:2986`) | **extends** | Per-tab TTL, `se.viewers > 0` pin, zero-tab `emptySince` branch — all implemented. **It deletes `m.sessions` entries only**; it never touches the coordinator and never closes a browser (verified: the body's only removal is `delete(m.sessions, sessionID)`). Whole-Chrome idle close is new (FR-040) |
| `controlledResult` (`tools.go:962-963`) | **modifies** | `mgr.Live().IsControlled(defaultSessionID)` — hardcoded. Silently returns `false` forever once the live registry is re-keyed (FR-002c) |
| `ListTabsTool` (`tabs.go:28-68`) | **modifies** | Already returns `browser_started` (`:58`, `:66`); description at `:31-39` |
| `captureRegistry` (`browser_webrtc.go:70-78`) | **modifies** | `sessions map[string]*browser.CaptureSession // keyed by agentID` (FR-016a) |
| `pickWarmBrowserManager` (`gateway.go:3373`, called `:3562`) | **modifies** | Selects by `mgr.AgentID()`, preferring `agents.defaults.default_agent_id`, else lexicographically-first agent id. Both halves break under the re-key (FR-016b) |
| `config.CaptureSharedContext` (`config.go:3849`, seeded `defaults.go:671`) | **retires** | FR-031. Its doc comment (`config.go:3800-3849`) also directs operators to the ADR-061-deleted JPEG fallback and must go with it |
| `tools.ToolWorkspaceID` (`pkg/tools/base.go:250`) | **reuses** | Set only when `ts.opts.WorkspaceID != ""` |
| `tools.ToolTranscriptSessionID` (`base.go:200`) | **not used** | §0.2 — not a browsing key |
| `workspace.FindForAgentPreferring` (`find_for_agent.go:176`) | **reuses** | Preferred-id fast path → `FindForAgent` (`:83`); sorted-first tie-break + WARN documented at `:45-48` |
| `ensureDefaultWorkspace` (`rest_workspaces.go:468`) / `defaultWorkspaceTeam` (`rest_workspace_delegation.go:359-379`) | **reuses** | Seeds "My Workspace" with `coreagent.All() ∩ configured agents` — Jim and Ray included — on every boot (`gateway.go:5013`) |
| `pkg/agent/tool_denial.go:206-210` | **modifies** | `policy_denied` → `ModelMessage: "Tool execution denied by policy."` — generic for every tool in the system (FR-014a) |
| `AgentLoop` `AutoDenyAsk` (`loop.go:594-599`, honoured at `loop.go::runTurn`'s `ts.opts.AutoDenyAsk` branch) | **reuses** | Set true only for headless/scheduled runs (`loop.go:6958`); **not inherited by delegated sub-turns — issue #659** (FR-032) |
| `BrowserAttachFrame.yaml`, `BrowserWebRTCOfferFrame.yaml`, `BrowserInspectRequest.yaml` | **modifies (prose)** | See FR-016/FR-017 and §15 MAJ-004 — one of the three is a *semantic* reversal, not a cosmetic edit |

### 2.2 Every `DefaultSessionID` consumer (the enumeration the previous draft was missing)

**37 non-comment references**, all of which must address the resolved browsing key (FR-002b). Counted with `grep -rn "DefaultSessionID\|defaultSessionID" pkg --include "*.go" | grep -v "_test\.go"` and filtering comment lines:

| File | Executable references | Count |
|---|---|---|
| `pkg/tools/browser/tools.go` | `:127, :236, :295, :433, :517, :659, :776, :883`, plus `controlledResult` at `:963` | 9 |
| `pkg/tools/browser/tabs.go` | `:58, :59, :117, :175, :258, :278` | 6 |
| `pkg/tools/browser/inspect.go` | `:158` (payload field), `:164`, `:194` | 3 |
| `pkg/tools/browser/capture_session.go` | `:518`, `:727` | 2 |
| `pkg/tools/browser/live.go` | `:380` (the empty-session-id default) | 1 |
| `pkg/gateway/browser_ws.go` | `:1266` Attach, `:1396` Input, `:1549` TakeControl, `:1564` ReleaseControl, `:1622` Controller, `:1637` SwitchTab, `:1647` CloseTab, `:1652` OpenTab, `:1724` Controller, `:1725` Detach, `:1858` Controller, `:1907` SetViewport, `:1945` CSSViewport | 13 |
| `pkg/gateway/browser_webrtc.go` | `:561` CSSViewport, `:1026` Input | 2 |
| `pkg/gateway/gateway.go` | `:3609` (warm path `mgr.Session`) | 1 |
| **Total** | | **37** |

Plus the declarations themselves (`tools.go:50-69`, `DefaultSessionID` and its package-private alias `defaultSessionID`) and 12 comment references, which must be deleted or corrected with them.

**Stream E's previous scope named three call sites.** It was short by an order of magnitude, and the three it named were the `BrowserManagerForAgent` calls, not these.

### 2.3 Impact assessment

| Symbol modified | Risk | Direct dependents (d=1) | Indirect (d=2) |
|---|---|---|---|
| `BrowserCoordinator` → pool | **CRITICAL** | `Register`, `Release`, `RemoveAgent`, `ensureLaunched`, `launchChrome`, `watchForCrash`, `PID`, launch lock, ownership marker, `TryOpenTab` | crash recovery, hot reload, boot warm, every live surface, host memory |
| `browserMgrs` keying | **CRITICAL** | `BrowserManagerForAgent`, `BrowserManagers`, reload prune, `Close` | all 3 gateway surfaces, every browser tool, reaping |
| `RegisterTools` manager binding | **CRITICAL** | 11 tool structs, `registerSharedTools` | **handover itself** — the reported defect |
| reload prune predicate | **CRITICAL** | `RemoveAgent` → browser close | every login on every workspace, on every Settings save |
| session-id argument at every consumer | **HIGH** | 37 sites (§2.2) | live view attach, control lock, tab strip, capture target |
| `controlledResult` | **HIGH** | 7 gated action tools | **ADR-038 D6 take-the-wheel — a shipped safety property** |
| gateway `BrowserManagerForAgent` | **HIGH** | `browser_webrtc.go`, `browser_ws.go:1252`, `browser_inspect.go:73` | capture registry, panel attach |
| `ListTabs` return shape | MEDIUM | `ListTabsTool`, `browser_inspect` | model-visible answer — the §1.1 defect |
| `capture_shared_context` retirement | MEDIUM | `Register`, `SetCaptureSharedContext`, `CaptureSharedContextEnabled`, `ApplyRuntimeConfig`, `coordinator_test.go:328` | operator config compatibility |

**Verified numeric corrections to the ADR:** the phrase "the shared browser session" occurs **8** times in the tree — **5** model-visible (`tabs.go:32,86,143,206` descriptions + `tools.go:415` parameter description), **2** Go comments (`tabs.go:19,186`), **1** unrelated SPA comment (`src/store/ui.ts:135`). The ADR says "six tool descriptions". Use the enumerated list, not the count.

---

## 3. Implementation Streams

Seven streams. **Stream A is the critical path and must land its interface first**; B–F code against it. **Stream P (the pool) is gated on G-1 and G-2 (§0.3)** and is the largest.

### 3.1 Shared interface contract (Stream A's first commit)

```go
// pkg/tools/browser/key.go (new)

// BrowsingKey is the identity of ONE browser: one Chrome process, one profile
// directory, one BrowserManager, one tab set. It replaces the DefaultSessionID
// constant as the thing every browser tool addresses. Constructed ONLY by
// ResolveBrowsingKey — there is deliberately no exported literal constructor
// and no zero-value default, so a caller cannot mint a shared browser by
// accident (ADR-072 D1.4).
//
// There is exactly ONE shape: "ws:<workspaceID>". The D1.2 ruling (2026-08-31)
// removed the unattended shape; do not reintroduce a second kind without
// reopening that ruling.
type BrowsingKey struct{ s string }

func (k BrowsingKey) String() string { return k.s }
func (k BrowsingKey) IsZero() bool   { return k.s == "" }

// WorkspaceID returns the workspace this key names. Used by audit and by the
// profile-directory path; never a branch in isolation logic.
func (k BrowsingKey) WorkspaceID() string

// ErrNoBrowsingContext is the D1.4 named failure. It MUST be returned — never
// swallowed into a shared browser, never mapped to a constant, never
// nil-with-empty. Its Error() text is a behavioural contract (FR-008).
var ErrNoBrowsingContext = errors.New(
    "browser: this turn is not rooted in a workspace, so it has no browser of its own; " +
        "add this agent to a workspace's team, or run the request in a workspace chat")

// ErrBrowserPoolFull is the FR-039 refusal. The pool NEVER evicts a live
// browser to make room — a silent eviction logs someone out mid-task, which is
// strictly worse than a refusal that names the cap.
var ErrBrowserPoolFull = errors.New(
    "browser: the maximum number of concurrent workspace browsers is already open; " +
        "close a browser panel or finish work in another workspace, then retry")
```

```go
// pkg/tools/browser/resolve.go (new) — the SINGLE resolution point.

// ResolveBrowsingKey decides which browser this turn's tools address. It is the
// ONLY function permitted to construct a BrowsingKey. Deterministic, pure apart
// from the workspace-file read FindForAgentPreferring performs.
//
// Ladder (ADR-072 D1.4), evaluated in order — three rungs, no fourth:
//   1. tools.ToolWorkspaceID(ctx) != ""   -> ws:<that id>
//   2. workspace.FindForAgentPreferring(home, tools.ToolAgentID(ctx), "")
//      resolves UNAMBIGUOUSLY                -> ws:<resolved id>
//   3. otherwise                             -> zero key + ErrNoBrowsingContext
//
// "Unambiguously" is load-bearing and is FR-033: when the agent is on the
// CoreTeam of two or more workspaces and no preferred id was supplied, the
// sorted-first tie-break that FindForAgent applies for FILESYSTEM re-rooting is
// NOT applied here, because here it would silently choose which set of live
// logins the turn acts with. Rung 2 refuses instead, with ErrNoBrowsingContext,
// and logs a WARN naming the candidates.
//
// Rung 1+2 are the pkg/tools/resolvepath.go:713 precedent so the browser and
// the work dir never disagree about which workspace a scheduled/heartbeat turn
// is rooted in. There is NO rung 4: a fallback constant re-creates the exact
// isolation regression ADR D1.4 rejects.
func ResolveBrowsingKey(ctx context.Context, home string) (BrowsingKey, error)
```

```go
// pkg/tools/browser/register.go — the manager is RESOLVED, not captured.
// This is FR-002a and it is the fix for the reported defect.

// ManagerResolver hands a tool the BrowserManager for the turn it is executing
// in. Implemented in pkg/agent over ResolveBrowsingKey + BrowserManagerForKey;
// faked in pkg/tools/browser tests. The interface lives in pkg/tools/browser
// and is implemented in pkg/agent because the import direction forbids the
// reverse — pkg/tools/browser cannot import pkg/agent.
type ManagerResolver interface {
    // ManagerFor resolves this turn's browser, launching it on first use.
    // Returns ErrNoBrowsingContext, ErrBrowserPoolFull, or a launch error.
    ManagerFor(ctx context.Context) (*BrowserManager, BrowsingKey, error)
}

// RegisterTools takes a resolver instead of constructing a manager. No tool
// struct holds a *BrowserManager after this change (FR-002a's structural test).
func RegisterTools(
    registry *tools.ToolRegistry,
    res ManagerResolver,
    evaluateEnabled bool,
    agentHome string,
    restrict bool,
) error
```

```go
// pkg/agent/loop.go — manager lookup replaces the per-agent map.

// BrowserManagerForKey returns (creating on first use) the manager that owns
// key's browser. Exactly one manager and one Chrome per key, process-wide.
// Returns ErrBrowserPoolFull when the cap is reached (FR-039).
func (al *AgentLoop) BrowserManagerForKey(ctx context.Context, k browser.BrowsingKey) (*browser.BrowserManager, error)

// BrowserManagerForAgent is RETAINED for the gateway. It resolves
// agentID -> BrowsingKey server-side using preferredWorkspaceID (from the
// attaching chat session's meta, FR-017) and delegates to BrowserManagerForKey.
// The second return distinguishes the failure reasons the panel must show
// differently (FR-008a) — it is NOT a bare bool.
func (al *AgentLoop) BrowserManagerForAgent(
    ctx context.Context, agentID, preferredWorkspaceID string,
) (*browser.BrowserManager, BrowserResolveOutcome)

// BrowserResolveOutcome is a closed enum. "not registered" and "no workspace"
// are DIFFERENT operator-facing problems and were indistinguishable before
// (browser_inspect.go:75-77 reported the former for both).
type BrowserResolveOutcome int
const (
    BrowserResolveOK BrowserResolveOutcome = iota
    BrowserResolveNoWorkspace   // ErrNoBrowsingContext
    BrowserResolveAmbiguous     // FR-033: >1 candidate workspace, no preference
    BrowserResolvePoolFull      // ErrBrowserPoolFull
    BrowserResolveNotRegistered // browser tools genuinely not registered
)
```

```go
// pkg/tools/browser/pool.go (new) — ADR-072 D1.1a. Stream P.

// BrowserPool owns N Chrome processes, one per BrowsingKey, replacing the
// coordinator's single-Chrome fields (rootCtx/rootCancel/cmd/launched/
// launching/launchDone/watchForCrash/PID and the launch lock + ownership
// marker, all of which become per-key).
//
// Isolation is the PROFILE DIRECTORY, not a CDP browser context: each Chrome
// launches with its own --user-data-dir, threaded through the existing
// pipeLaunchConfig.userDataDir seam (exec_resolver.go:385).
//
//   Profile dir:      <cfg.ProfileDir>/ws/<workspaceID>/
//   Launch lock:      <cfg.ProfileDir>/ws/<workspaceID>/chrome.lock
//   Ownership marker: $OMNIPUS_HOME/browser/ws-<workspaceID>.pid
//
// INVARIANT P-1: one live Chrome per key, enforced by per-key single-flight
//                (the existing launched/launching/launchDone triple, per entry).
// INVARIANT P-2: len(live) <= cap. Acquire REFUSES at the cap (FR-039); it
//                never evicts a live browser.
// INVARIANT P-3: no manager path ever calls chromedp.WithNewBrowserContext or
//                target.CreateBrowserContext. CDP browser contexts are retired
//                entirely (FR-031); ADR-043 CRIT-003 is preserved by deletion.
// INVARIANT P-4: one Chrome's death affects exactly one key (FR-041).
type BrowserPool struct{ /* ... */ }

// Acquire returns the live Chrome for k, launching it if absent.
func (p *BrowserPool) Acquire(ctx context.Context, k BrowsingKey) (*chromeInstance, error)

// Close shuts down k's Chrome and releases its entry. Idempotent. The ONLY
// disposal path (FR-040, FR-026a, FR-026c).
func (p *BrowserPool) Close(k BrowsingKey)

// LiveKeys / PID are the test and audit accessors. PID is per key now; the
// coordinator's PID() int (coordinator.go:849) does not survive.
func (p *BrowserPool) LiveKeys() []BrowsingKey
func (p *BrowserPool) PID(k BrowsingKey) (int, bool)
```

**Locking discipline (load-bearing).** The pool's bookkeeping mutex is never held across a Chrome launch, a CDP call, or a `Close`; per-key single-flight uses the existing `launching`/`launchDone` condition-variable pattern rather than holding the pool lock. Lock order is `writeLease → pool.mu → m.mu`, never the reverse, and `m.mu` is never held across `acquireWrite` (§14) or any CDP call — the ADR-038 discipline, unchanged.

### 3.2 Streams

**Stream A — Key + resolution + per-Execute manager binding [CRITICAL PATH, not gated].**
Owns `key.go`, `resolve.go` (new); `ManagerResolver` and the `RegisterTools` signature change plus all 11 tool structs (FR-002a); the `browserMgrs` re-key and `BrowserManagerForKey`/`BrowserManagerForAgent` (`loop.go`); **`controlledResult`'s re-key (FR-002c — it is on the resolution path, not the lease path)**; all 37 `DefaultSessionID` consumers (FR-002b) and the constant's deletion; the reload-prune predicate and per-key idempotent registration (FR-026a, FR-026b); the `loop.go:270-279` comment's replacement (FR-002d).
Depends on: nothing. Interface out: §3.1.

**Stream P — Browser-process pool [GATED on G-1 + G-2; largest].**
Owns `pool.go`; the coordinator's decomposition into per-key instances; per-key profile dirs, launch locks and ownership markers (FR-037, FR-042); the cap and its refusal (FR-038, FR-039); whole-Chrome idle close (FR-040); per-Chrome crash containment, replacing `watchForCrash`'s reset-everything behaviour (FR-041); profile-based reload survival replacing ADR-043 CRIT-002's context re-adoption (FR-043); retirement of `capture_shared_context`, `disposeBrowserContextRaw`, `contextCount()` and the CDP-context branch of `Register` (FR-031).
Depends on: A's key type. **Do not start before G-2 passes.**

**Stream C — Three-state tabs + descriptions (D1.5) [depends on A].**
Owns `ListTabsState` + `ListTabs` delegation (`manager.go:1605-1613`); `ListTabsTool.Execute` (`tabs.go:48-68`); the five model-visible strings and their **specified replacements** (FR-015, FR-034) and the two Go comments (`tabs.go:19,186`).
Does **not** own the "not permitted" state — that is the policy layer (FR-014, FR-014a).

**Stream D — Write lease [depends on A].** Owns `lease.go` per **§14**, the call pairs in every mutating tool, and composition with `controlledResult`. **§14 is normative; this stream implements it and the D2 spec references it.**

**Stream E — Gateway resolution + contracts [depends on A].**
Owns the three `BrowserManagerForAgent` call sites; server-side agent→workspace resolution preferring the attaching session's `workspace_id` (`pkg/session/unified_meta_files.go:60`); the capture registry's re-keying and the ADR-048 conflict rule's collapse (FR-016a); the boot warm path (FR-016b); the panel's failure messages (FR-008a); the three schema description edits, **one of which is a semantic reversal and must be reviewed as one** (FR-016, MAJ-004 in §15).

**Stream F — Audit + lifecycle [depends on A, P].**
Owns the audit events (FR-027) and their provenance assertion (FR-035); disposal on workspace deletion and roster change (FR-026); the reaper interactions (FR-025) and the pool's idle-close hook (FR-040); `#659`'s auto-deny requirement for delegated sub-turns (FR-032).

**Stream G — Tests + regression (cross-cutting).** Owns §10.

**Parallelization:** A lands its interface first. C/D/E/F fan out on disjoint files. P runs behind its gates and lands last, because until it does the product has one Chrome and therefore no cookie partitioning — which is exactly today's behaviour, so the intermediate state is not a regression.

---

## 4. Behavioral contract (observable)

1. **Handover.** The operator browses in a workspace chat with Mia and switches the chat to Jim; Jim's `browser_list_tabs` returns the same tabs, with no handover command and no re-navigation.
2. **Human-first.** The operator opens the live panel and browses before addressing any agent; any browser-policy-allowed agent on that workspace then sees the tab.
3. **Cross-workspace isolation.** A site login established in workspace X is absent when the same site is opened in workspace Y, because X and Y are different Chrome processes with different profile directories.
4. **No surprise logout.** A login established in one chat is still present in a new chat in the same workspace.
5. **Delegated work shares the browser.** A delegated sub-turn — attended or not — uses its workspace's browser and its logins. (D1.2. This **inverts** the previous draft's contract item 5.)
6. **Workspace-less turn.** A scheduled or heartbeat turn whose `ToolWorkspaceID(ctx)` is empty but whose work dir was re-rooted into a CoreTeam workspace reaches **that same workspace's** browser.
7. **Genuine no-workspace.** An agent on no workspace at all gets `ErrNoBrowsingContext`'s named text from every browser tool — never a shared browser, never an empty success.
8. **Ambiguous workspace.** An agent on two or more workspaces, on a turn carrying no workspace id, is **refused** with the ambiguity named — the sorted-first tie-break is not applied to a browser (FR-033).
9. **Three states.** `browser_list_tabs` distinguishes "no browser here yet", "a browser with these tabs" and "a browser that momentarily has none" without inference.
10. **Not permitted.** A policy-denied agent asked what is open receives a denial that **names the browser surface** — never "there are no tabs" (FR-014a).
11. **One writer.** Two agents on one workspace issuing action tools concurrently: neither observes the other's mid-action state; the loser receives a **non-error** `{"deferred": true, "reason": …}`.
12. **Human outranks agent.** While a human holds the live-view control lock, an agent action tool defers with the ADR-038 D6 reason, not the lease reason.
13. **No wedge.** An action tool that panics, times out or is cancelled while holding the lease does not prevent the next one from acquiring it.
14. **Live panel.** Every gateway surface resolves the manager that owns the browser that agent's turns use for the attaching chat session — and when it cannot, says *which* reason (FR-008a).
15. **Reload.** A Settings save mid-browse leaves each workspace's Chrome pid unchanged and its login intact.
16. **Audit.** Browser creation, and an agent's first use of a browser it did not establish, are both recorded with the agent, the key and the workspace.
17. **Pool cap.** When the cap is reached, a request for a **new** workspace's browser is **refused** with `ErrBrowserPoolFull`; no live browser is closed to make room, and no user is logged out to serve someone else.
18. **Crash containment.** One workspace's Chrome dying leaves every other workspace's browsing unaffected — its tabs, its panel and its logins survive.
19. **Idle close.** A workspace browser with no tabs and no viewers past the idle window is closed entirely, releasing its process; its profile (and therefore its logins) remains on disk for the next use.
20. **Unattended work cannot hang.** An `ask`-policy tool reached from a delegated sub-turn is **denied**, not queued against an operator who is not there (FR-032, #659).

---

## 5. Explicit non-behaviors

- The system must **not** fall back to `DefaultSessionID`, `""`, the agent id, or any other constant when workspace resolution fails. There is no default browser.
- The system must **not** apply `FindForAgent`'s sorted-first tie-break to a *browsing* key. It selects live credentials; for a filesystem mount the worst case is the wrong directory, here it is acting as the wrong signed-in identity (FR-033).
- The system must **not** give a delegated sub-turn a separate browser, a separate key, or a signed-out jar. That was reversed by ruling; reintroducing it is a design change requiring a new ADR entry, not an optimisation.
- The system must **not** construct a `BrowsingKey` anywhere except `ResolveBrowsingKey`, and must **not** add a second key shape.
- The system must **not** let any tool struct hold a `*BrowserManager` captured at registration (FR-002a).
- The system must **not** leave any consumer of `DefaultSessionID` behind; the constant is **deleted, not deprecated** (FR-002b).
- The system must **not** call `chromedp.WithNewBrowserContext` or `target.CreateBrowserContext` on any path. CDP browser contexts are retired outright (FR-031); ADR-043 CRIT-003 is preserved by deletion rather than by discipline.
- The system must **not** evict a live workspace browser to satisfy a new request. Refuse at the cap (FR-039).
- The system must **not** destroy a browser on hot reload. Only workspace deletion, roster change, idle close, cap-free explicit close, or gateway `Close()` (FR-026a, FR-040).
- The system must **not** delete a workspace's **profile directory** when its Chrome is closed for idleness — the logins are the point of the profile (FR-040, FR-043).
- The system must **not** return `nil, 0, nil` from `ListTabs` for a missing browser once `ListTabsState` exists.
- The system must **not** add, remove or retype any field in `contracts/` for D1. Descriptions change, and **one of those description changes is a semantic reversal** that must be reviewed as a behavioural contract change, not as prose (FR-016, §15 MAJ-004).
- The system must **not** widen the seeded browser tool policy. Mia and Ava stay denied.
- The system must **not** hold `m.mu` or `pool.mu` across `acquireWrite`, a Chrome launch, or any CDP call.
- The system must **not** change the `browser-webrtc[<agent>]` log label to a workspace label — cosmetic, and the agent is still the useful identity in a log line (round-1 review O3).
- The system must **not** re-key the `serve_web` preview URL. Out of scope, ADR §6 open.
- The system must **not** ship a `max_browsers` default derived from an estimate (FR-044).

---

## 6. Integration boundaries

- **Chrome processes / CDP.** The count of live Chromes now scales with **workspaces being actively browsed**, bounded by `max_browsers` (FR-038). Each is launched over the pipe transport (`exec_resolver.go`, `cdppipe`) with its own `--user-data-dir`. A launch failure surfaces as a tool error naming the workspace — never a silent join to another workspace's browser. **CDP browser contexts are no longer created at all** (FR-031).
- **Sandbox (Landlock/seccomp).** No new network surface: CDP flows over inherited fds 3/4, and the fixed DevTools port allow-rule was already removed (`pkg/gateway/sandbox_apply.go:405-417`). What *is* new is **N profile directories**, so the filesystem allow-list must cover `<cfg.ProfileDir>/ws/` as a subtree rather than a single profile path. Verify against `sandbox_apply.go`'s path rules before the pool lands — this is the one sandbox interaction that is genuinely new.
- **Host memory.** The binding cost, and the reason for the cap. G-1 (FR-044) measures it; ADR-043's "≈4–5 GB at ten" is unmeasured and per-agent, so it must not be quoted as the figure.
- **Workspace store** (`pkg/workspace/find_for_agent.go`): read-only. `FindForAgent` tie-breaks by sorted-first id (`:45-48`); `FindForAgentPreferring`'s fast path suppresses the ambiguity WARN (`:168-176`). FR-033 declines that tie-break for browsing keys and requires the WARN on **both** paths whenever it would have arbitrated.
- **Fresh install.** A fresh install is **not** workspace-less: `ensureDefaultWorkspace` (`rest_workspaces.go:468`, called at `gateway.go:5013` on every boot) creates "My Workspace" with `defaultWorkspaceTeam(cfg)` = `coreagent.All() ∩ configured agents` (`rest_workspace_delegation.go:359-379`), which includes Jim and Ray — the two browser-policy-allowed agents. So the default path resolves. **The residual case is a custom agent**: the system deliberately never auto-adds a custom/pre-existing agent to any workspace team (ADR-046 FR-008, stated at `gateway.go:5018-5025`), so a custom browser-allowed agent resolves to nothing and must be told why (FR-008a, US-14).
- **Session store** (`pkg/session/unified_meta_files.go:60`): the gateway reads `workspace_id` off the attaching chat session's meta. A session without one degrades to `FindForAgentPreferring(home, agentID, "")` — same ladder, same FR-033 refusal on ambiguity.
- **Policy engine** (`pkg/agent/tool_denial.go:206-210`): produces the third `browser_list_tabs` state. Today's `ModelMessage` is the generic `"Tool execution denied by policy."` for every tool in the system; FR-014a adds a browser-specific denial so ADR criterion 3b has an artefact that can be tested.
- **Delegation and `ask` policies (#659).** D1 establishes delegated sub-turns that browse a signed-in workspace with no operator present. `AutoDenyAsk` (`loop.go:594-599`) makes an `ask`-policy tool auto-deny, but it is set only for headless/scheduled runs (`loop.go:6958`) and **is not inherited by delegated sub-turns — issue #659, open**. D2.9 seeds `browser_upload_file` as `ask` for every agent. **If D2.9 ships without #659, the first delegated sub-turn to reach it hangs on an approval nobody can answer.** FR-032 makes the auto-deny a D1 requirement rather than a D2 assumption, because D1 is what creates the unattended browsing.
- **Capture / ADR-048** (`browser_webrtc.go:70-78`, `config.go:3826-3844`): the registry is `map[agentID]*CaptureSession`, and ADR-048's conflict rules ("bring the requesting agent's tab to front", "deny while another agent's session is actively viewed") assume agents have disjoint tab sets. Under D1 they share one. FR-016a re-keys the registry to the browsing key and collapses "requesting agent" — **one capture session per workspace browser**.
- **Audit** (`pkg/audit`): two new event types, existing severity/format conventions, no new sink.
- **SPA**: no wire field change, so no required SPA change. The `browser_list_tabs` payload changes from `browser_started: bool` to `state: enum`, which alters JSON persisted in `sessions/<id>/<YYYY-MM-DD>.jsonl`; verified safe — `grep -rn "browser_started" src/` returns nothing, so the SPA does not read it. The `src/store/ui.ts:135` comment wording is cosmetic.
- **Platform (Windows/POSIX):** the write lease is an in-process `sync` primitive, real everywhere, and deliberately **not** `fileutil.WithFlock` (a documented no-op on Windows, `pkg/fileutil/flock_windows.go`). The **pool's per-key launch lock is a different matter**: it *is* an on-disk lock, and it inherits the Windows no-op. Two gateway processes on one `$OMNIPUS_HOME` are already unprotected for every other file store on Windows (ADR-054 §5.1); the pool does not make that worse and does not fix it (§12, A11).

---

## 7. User stories & acceptance criteria

**US-1 (P0) Agent handover.** As an operator, when I switch the chat from one agent to another mid-session, the new agent sees and drives the browser I was just using, with no handover step.
- *Why P0:* the reported defect (ADR §1.1); ADR criterion 2.
- **AC1: Given** workspace W's browser holds one tab, **When** an agent on W's team with `browser_list_tabs: allow` calls it, **Then** that tab is returned regardless of which agent opened it.
- **AC2: Given** the same, **When** the operator issues no handover command of any kind, **Then** AC1 still holds.
- **AC3 (the seam that actually carries this): Given** two agents on W, **When** both call a browser tool, **Then** both `Execute` calls resolve to the **same** `*BrowserManager` instance, obtained through `ManagerResolver` at call time — not one captured when their tools were registered.

**US-2 (P0) Human browses first.** As an operator, I open the browser panel and browse before deciding which agent to ask, and an agent still takes over.
- **AC1: Given** the operator browsed via the live panel in workspace W, **When** a browser-allowed agent on W is asked what is open, **Then** it sees the operator's tab.
- **AC2:** the panel path and the tool path resolve the same manager for the same (agent, chat session) pair.

**US-3 (P0) Cross-workspace isolation.** As an operator with two clients in two workspaces, a login in one is invisible in the other.
- *Why P0:* ADR criterion 5b.
- **AC1: Given** a login established in workspace X, **When** the same site is opened from workspace Y, **Then** Y is logged out **and** X's and Y's Chrome **pids differ** **and** their `--user-data-dir` paths differ.
- **AC2: Given** the same, **Then** neither Chrome was created via `target.CreateBrowserContext` — `contextCount()` no longer exists and no CDP context is created anywhere (FR-031).

**US-4 (P0) No surprise logout.** A new chat in the same workspace is still logged in.
- **AC1: Given** a login established in chat C1 of workspace W, **When** a new chat C2 in W opens the same site, **Then** it is still logged in **and** both resolve to the same Chrome pid. (ADR criterion 5c.)

**US-5 (P0) Delegated work shares the workspace browser.** As an operator, work I delegate uses the same signed-in browser as the chat I delegated from.
- *Why P0:* ADR criterion 17, **inverted** by the D1.2 ruling — it now asserts sharing, so a future change that silently isolates delegated work fails here.
- **AC1: Given** workspace W's browser holds a login, **When** a delegated sub-turn under `spawnSubTurn` opens that site, **Then** it is **logged in** and its resolved key is `ws:W`.
- **AC2: Given** the same, **Then** no second Chrome, no second manager and no second profile directory was created for the sub-turn.
- **AC3 (the risk this accepts, made testable): Given** a delegated sub-turn reaches a tool whose policy is `ask`, **When** no operator is attached, **Then** the call is **denied** with the headless auto-deny reason — never queued for an approval nobody can answer. (FR-032; depends on #659.)

**US-6 (P0) A workspace-less turn resolves, never merges.** A heartbeat turn's browser is the same workspace's browser as its files.
- **AC1: Given** a scheduled turn with `ToolWorkspaceID(ctx) == ""` whose work dir was re-rooted into workspace W, **When** it calls a browser tool, **Then** it reaches W's browser — the same id `FindForAgentPreferring` gave `resolvepath.go:713`.
- **AC2: Given** two agents on **no** workspace, **When** each opens the same site, **Then** neither sees the other's session, because **both calls fail** with `ErrNoBrowsingContext`.
- **AC3: Given** an agent on the CoreTeams of workspaces A and B and a turn carrying no workspace id, **When** it calls a browser tool, **Then** it is **refused** with the ambiguity named and both candidates logged at WARN — **not** silently given A. (FR-033.)

**US-7 (P0) Three tab states.** As an agent, I can tell "no browser here yet" from "a browser with nothing open".
- **AC1:** no browser for the resolved key → `state: "no_context"`, and the model-visible text says so.
- **AC2:** a live browser with ≥1 tab → `state: "open"` with the tabs.
- **AC3:** a live browser momentarily with 0 tabs → `state: "empty"`, distinct from AC1.
- **AC4:** the three values are the complete closed set; no fourth value is emitted for any input.

**US-8 (P1) A denied agent says it is denied — about the browser.** As an operator, Mia tells me she is not allowed to see the browser, not that there are no tabs.
- *Why P1 but load-bearing:* ADR criterion 3b exists so §1.1's symptom does not recur with a new cause.
- **AC1: Given** an agent whose policy denies `browser_list_tabs`, **When** it attempts the call, **Then** it receives a denial and **no** tab payload, and `ListTabsTool.Execute` was never entered.
- **AC2: Given** the same, **Then** the `ModelMessage` **names the browser surface** — e.g. *"This agent's tool policy does not allow the browser tools."* — rather than the generic `"Tool execution denied by policy."` that every other denied tool in the system produces (`tool_denial.go:206-210`). (FR-014a.)
- **AC3 (manual, required):** the UAT transcript in §13 holdout 4 is recorded and reviewed. **No automated test proves what the model *says*; AC2 is the strongest artefact-level proxy available and the spec says so rather than implying coverage it does not have.**

**US-9 (P0) Two writers, one browser.** Concurrent browser work by two agents on one workspace neither corrupts a page nor errors.
- **AC1:** two agents issuing `browser_navigate` concurrently — neither observes the other's mid-navigation state, neither returns `IsError=true`, exactly one gets `{"deferred": true, …}`.
- **AC2:** a human holding the live-view control lock outranks the lease; the deferral reason is ADR-038 D6's text and the lease was never acquired.
- **AC3:** an action tool that panics or is cancelled while holding the lease does not prevent the next acquire within `leaseWaitTimeout`.
- **AC4:** exempt tools are never deferred. The exempt set is **five**: three read-only (`browser_screenshot`, `browser_get_text`, `browser_wait`), plus `browser_snapshot` (read-only, D2 FR-018) and `browser_handle_dialog` (**recovery** — D2 FR-035; it must be able to clear a wedge held by a blocked `browser_click`, so leasing it would make it defer behind the very thing it exists to unwedge).
- **AC5:** **every** tool in `pkg/tools/browser` that mutates page or tab state acquires the lease; the exemption list is exactly the five in AC4. A tool that is neither leased nor exempt fails the gate (FR-019a).

**US-10 (P1) The wire contract holds, and its one semantic change is visible.** As a maintainer, no field changes — and the meaning change that *does* happen is not smuggled through as prose.
- **AC1:** `make verify-contracts` exits 0 with no `properties:`/`required:`/`enum:`/`type:` change.
- **AC2:** `BrowserAttachFrame.session_id` and `BrowserWebRTCOfferFrame.session_id` change from correlation-only to **workspace-resolving**. Their current text says the server binds *"regardless of the value sent here"* and *"agent_id is the binding key"*; the replacement text is quoted verbatim in FR-016 and reviewed as a behavioural change.
- **AC3:** `BrowserInspectRequest.session_id` is a **browser** session id, not a chat session id (`"Browser session id (context/correlation; the live tab is the agent's default)"`). It therefore does **not** gain workspace semantics; `browser_inspect` resolves from the agent alone, subject to FR-033's ambiguity refusal, and its description is corrected to say so.
- **AC4:** a test asserts the reversal — a client sending a `session_id` belonging to a different workspace's chat is routed to that workspace or refused, **never** silently served the agent's default. Without this, the gate cannot fail for the change it polices.

**US-11 (P1) An agent on two workspaces is unambiguous in the panel.** The panel and the agent show the same browser.
- **AC1: Given** an agent on the CoreTeam of workspaces A and B, **When** the operator chats in a session stamped `workspace_id=B` and opens the panel, **Then** both the turn's tools and the panel resolve to B.
- **AC2: Given** no session `workspace_id`, **Then** both refuse identically (FR-033) — they never disagree, and they never both silently pick A.

**US-12 (P1) Memory stays bounded; browsers get closed.** Workspace-keying must not leak Chrome processes.
- **AC1:** tabs idle past `IdleTTL` with no viewer are reaped, per-tab, unchanged.
- **AC2:** one viewer attached in one chat of workspace W pins W's whole browser — the documented, accepted consequence of the re-key.
- **AC3:** a workspace is deleted, or loses every browser-policy-allowed agent from its CoreTeam → its Chrome is closed and its pool entry released.
- **AC4:** a workspace browser with zero tabs and zero viewers past the idle window is **closed entirely** (process gone, `LiveKeys()` shrinks) while its **profile directory survives** on disk.
- **AC5:** running K delegated sub-turns to completion returns `len(LiveKeys())` and the manager count to their pre-run values — sub-turns create no browser of their own (US-5/AC2) and therefore leak none.

**US-13 (P1) Repudiation.** As an operator, I can answer "which agent acted as the signed-in user".
- **AC1:** a browser's creation records key, workspace and establishing agent.
- **AC2:** an agent's **first** action in a browser it did not establish records agent, key and workspace. Subsequent actions by that same agent are not re-recorded — accepted, and stated so it is not mistaken for full action-level audit.

**US-14 (P1) An agent with no workspace is told the truth.** As an operator, when the browser will not work I learn *why*.
- *Context:* a fresh install **is** covered — `ensureDefaultWorkspace` seeds "My Workspace" with Jim and Ray (§6). The gap is a **custom** agent, which is deliberately never auto-added to a team.
- **AC1: Given** a custom browser-allowed agent on no workspace, **When** it calls a browser tool, **Then** the error is `ErrNoBrowsingContext`'s text, which names the remedy ("add this agent to a workspace's team").
- **AC2: Given** the same agent, **When** the operator attaches the live panel, **Then** the panel shows a reason distinguishing *"this agent is not on a workspace"* from *"browser tools are not registered for this agent"* — today both render as the latter (`browser_inspect.go:75-77`, `browser_ws.go:1252-1262`). (FR-008a.)
- **AC3:** the pool-full and ambiguous cases each render their own distinct reason (`BrowserResolveOutcome`).

**US-15 (P0) The pool is bounded and never logs anyone out to make room.** As an operator on a sized host, opening an eleventh workspace must not close my tenth.
- **AC1: Given** `max_browsers = N` and N live browsers, **When** a turn resolves to an (N+1)th workspace, **Then** it fails with `ErrBrowserPoolFull`'s named text and **no** live browser is closed.
- **AC2: Given** the same, **Then** the N live browsers' pids are unchanged and no session cookie anywhere was lost.
- **AC3:** `max_browsers`' shipped default is derived from the G-1 measurement and the measurement is recorded in the PR (FR-044).

**US-16 (P0) One crash is one workspace.** As an operator, a Chrome crash in one workspace does not stop work in another.
- *Why P0:* ADR-043 accepted "one crash takes down all browsing" when there was one Chrome. With N that acceptance no longer holds.
- **AC1: Given** workspaces W1 and W2 each with a live browser and an attached viewer, **When** W1's Chrome is killed, **Then** W2's pid, tabs, panel stream and login are unaffected.
- **AC2: Given** the same, **When** W1's next browser tool runs, **Then** it relaunches W1's Chrome from W1's **profile directory** — so W1's login survives the crash, which a CDP context could not do (`coordinator.go`'s `watchForCrash` comment: *"recovery is into FRESH empty contexts — prior per-agent cookies/login are lost by definition"*).
- **AC3:** no other workspace's managers are reset by W1's crash — today `watchForCrash` resets **every** connector manager.

**US-17 (P1) Reload preserves every workspace's login.** A Settings save mid-browse changes nothing an operator can see.
- **AC1: Given** two agents on workspace W and a live login, **When** `ReloadProviderAndConfig` runs, **Then** W's Chrome pid is unchanged, the login persists, and `Close`/`disposeBrowserContextRaw` was called **zero** times.
- **AC2: Given** N agents on one workspace, **When** one reload runs, **Then** exactly **one** register/release cycle occurs for that key — not N (FR-026b).

---

## 8. BDD scenarios

**Scenario: handover across an agent switch (Happy Path) — US-1/AC1+AC2, FR-001, FR-002, FR-006**
- **Given** workspace W's browser has one tab on `https://example.com/a`
- **And** Mia and Jim are both on W's CoreTeam, and Jim has `browser_list_tabs: allow`
- **When** Jim calls `browser_list_tabs` in a chat stamped `workspace_id=W`
- **Then** the result is `state:"open"` with one tab whose url is `https://example.com/a`
- **And** no handover tool was called

**Scenario: two agents resolve one manager through the real registration path (Happy Path) — US-1/AC3, FR-002a**
- **Given** agents Mia and Jim were registered through `registerSharedTools`, each with their own tool registry
- **And** both are on workspace W's CoreTeam
- **When** Mia's `browser_navigate` opens a tab and Jim's `browser_list_tabs` runs
- **Then** both `Execute` calls resolved the **same** `*BrowserManager` pointer
- **And** no tool struct in `pkg/tools/browser` has a `mgr *BrowserManager` field

**Scenario: human browses first, then an agent takes over (Happy Path) — US-2/AC1+AC2, FR-016, FR-017**
- **Given** no agent has used the browser in workspace W
- **When** the operator attaches the live panel with `agent_id=mia`, `session_id=S` where S's meta carries `workspace_id=W`, and navigates to `https://example.com/a`
- **And** Jim is then asked, in a chat also stamped `workspace_id=W`, to list tabs
- **Then** Jim sees `https://example.com/a`
- **And** the panel and Jim resolved the same manager

**Scenario: cross-workspace isolation by profile (Error-free isolation) — US-3, FR-003, FR-004, FR-037**
- **Given** a login cookie set on `example.com` in workspace X
- **When** `example.com` is opened from workspace Y
- **Then** the document reports no session cookie
- **And** `pool.PID(ws:X) != pool.PID(ws:Y)` and their `--user-data-dir` paths differ
- **And** no `target.CreateBrowserContext` call was made anywhere in the run

**Scenario: new chat, same workspace, still logged in — US-4/AC1, FR-005**
- **Given** a login cookie set on `example.com` in chat C1 of workspace W
- **When** a new chat C2 in W opens `example.com`
- **Then** the session cookie is present **and** `pool.PID(ws:W)` is the same for both

**Scenario: a delegated sub-turn shares the workspace browser (the inverted criterion) — US-5/AC1+AC2, FR-009**
- **Given** workspace W's browser holds a login on `example.com`
- **And** a delegated sub-turn is running under `spawnSubTurn` from a chat stamped `workspace_id=W`
- **When** the sub-turn navigates to `example.com`
- **Then** its resolved key is `ws:W`, the session cookie **is** present
- **And** `len(pool.LiveKeys())` did not increase and no second manager was created

**Scenario: an unattended sub-turn is denied, not hung, on an ask-policy tool (Error) — US-5/AC3, FR-032**
- **Given** a delegated sub-turn with no operator attached
- **And** a tool whose resolved policy is `ask`
- **When** the sub-turn calls it
- **Then** the call is denied with the headless auto-deny reason within the turn
- **And** no approval request was created and the turn did not block

**Scenario: scheduled turn resolves to its re-rooted workspace — US-6/AC1, FR-007**
- **Given** a heartbeat turn for agent `ray`, member of exactly one workspace W, with `ToolWorkspaceID(ctx) == ""`
- **When** it calls `browser_navigate`
- **Then** `ResolveBrowsingKey` returns `ws:W`, the same id `FindForAgentPreferring(home,"ray","")` returns
- **And** the key is never `"default"` and never the agent id

**Scenario: a genuinely workspace-less agent is refused, not merged (Error) — US-6/AC2, FR-008**
- **Given** agents `solo-a` and `solo-b`, neither on any workspace CoreTeam
- **When** each calls `browser_navigate`
- **Then** each result is an error whose text is `ErrNoBrowsingContext`'s
- **And** no browser was launched for either, and `len(pool.LiveKeys())` is unchanged

**Scenario: an ambiguous multi-workspace turn is refused, not arbitrated (Error) — US-6/AC3, US-11/AC2, FR-033**
- **Given** agent `ray` on the CoreTeams of workspaces A and B (A sorts before B)
- **And** a turn carrying no `workspace_id` and no preferred id
- **When** it calls `browser_navigate`
- **Then** the result is an error naming both candidate workspaces
- **And** a WARN was emitted **on the preferring path as well as the plain one**
- **And** neither A's nor B's browser was launched

**Scenario: three tab states are distinguishable (Edge Case) — US-7, FR-013**
- **Given** a resolved key with no browser, **When** `browser_list_tabs` runs, **Then** `state:"no_context"` with an empty `tabs` array
- **And given** a live browser with two tabs, **Then** `state:"open"` with two entries
- **And given** a live browser whose tab set is momentarily empty (post-`CloseTab`, pre-`createFirstTab`), **Then** `state:"empty"` with an empty array
- **And** the three payloads are pairwise unequal

**Scenario: a denied agent is told it is denied about the browser (Error) — US-8/AC1+AC2, FR-014, FR-014a**
- **Given** agent `mia`, whose seed (`pkg/coreagent/core.go:848`) grants no `browser_*` entry
- **When** she attempts `browser_list_tabs`
- **Then** `ListTabsTool.Execute` was never entered and no tab payload was produced
- **And** the `ModelMessage` names the browser surface and is **not** the generic `"Tool execution denied by policy."`

**Scenario: two agents write concurrently; the loser defers, nobody errors — US-9/AC1, FR-019, FR-020**
- **Given** Jim and Ray on workspace W, both with `browser_navigate: allow`
- **When** both call `browser_navigate` against W's browser within the same millisecond
- **Then** exactly one navigation is observed by Chrome
- **And** the other returns `IsError=false` with a body parsing to `{"deferred": true, "reason": <non-empty>}`
- **And** neither result is a Go error

**Scenario: human control outranks the lease — US-9/AC2, FR-022, FR-002c**
- **Given** a human viewer holds the control lock on W's browser, taken under the key `ws:W`
- **When** an agent calls `browser_click`
- **Then** `controlledResult` detected the lock — having asked `IsControlled(ws:W)`, not `IsControlled("default")`
- **And** the deferral reason is ADR-038 D6's human-control text and the lease was never acquired

**Scenario: a panicking action tool does not wedge the browser — US-9/AC3, FR-024**
- **Given** an action tool acquires the lease and then panics (or its ctx is cancelled)
- **When** another action tool runs on the same browser
- **Then** it acquires the lease within `leaseWaitTimeout` and completes normally

**Scenario: read-only tools are never deferred — US-9/AC4, FR-021**
- **Given** Jim holds the write lease on W's browser for a long navigation
- **When** Ray calls `browser_screenshot`, `browser_get_text` and `browser_wait`
- **Then** all three execute; none returns a `deferred` body

**Scenario: every mutating tool is leased (structural) — US-9/AC5, FR-019a**
- **Given** the tool registry after `RegisterTools`
- **When** each registered `browser_*` tool is classified
- **Then** every tool that mutates page or tab state acquires the lease
- **And** the unleased set is exactly `{browser_screenshot, browser_get_text, browser_wait, browser_snapshot, browser_handle_dialog}`

**Scenario: the pool refuses at the cap rather than evicting — US-15, FR-038, FR-039**
- **Given** `max_browsers = 2` and live browsers for workspaces W1 and W2, W1 holding a login
- **When** a turn resolves to workspace W3
- **Then** the tool result is an error whose text is `ErrBrowserPoolFull`'s
- **And** `pool.PID(ws:W1)` and `pool.PID(ws:W2)` are unchanged and W1's cookie is still present

**Scenario: one Chrome crash is one workspace — US-16, FR-041**
- **Given** live browsers for W1 and W2, each with an attached viewer
- **When** W1's Chrome process is killed
- **Then** `pool.PID(ws:W2)` is unchanged, W2's tabs and viewer stream survive, and W2's manager was not reset
- **And when** W1's next browser tool runs, **then** W1's Chrome relaunches from W1's profile dir and W1's login is still present

**Scenario: idle close frees the process but keeps the profile — US-12/AC4, FR-040**
- **Given** workspace W's browser has zero tabs and zero viewers past the idle window
- **When** the reaper sweeps
- **Then** W's Chrome process is gone and `pool.LiveKeys()` no longer contains `ws:W`
- **And** W's profile directory still exists on disk
- **And when** W is used again, **then** the relaunched browser is still logged in

**Scenario: tabs reap per-tab and a viewer pins the whole browser — US-12/AC1+AC2, FR-025**
- **Given** W's browser has two tabs, one idle past `IdleTTL`, and one attached viewer
- **When** the reaper sweeps
- **Then** the idle tab is reaped, the other is not, and W's browser is not closed

**Scenario: K delegated sub-turns leak nothing — US-12/AC5, FR-026c**
- **Given** baseline `len(pool.LiveKeys())` and manager count recorded
- **When** K delegated sub-turns each perform a browser action and terminate
- **Then** both counts equal their baselines

**Scenario: disposal on workspace deletion — US-12/AC3, FR-026**
- **Given** workspace W has a live browser
- **When** W is deleted and the reload prune runs
- **Then** `pool.Close(ws:W)` is called exactly once and `LiveKeys()` shrinks by one

**Scenario: reload preserves every workspace's login, once per key — US-17, FR-026a, FR-026b, FR-028**
- **Given** **two** agents on workspace W, a live login, and `pool.PID(ws:W)==P`
- **When** `ReloadProviderAndConfig` runs
- **Then** `pool.PID(ws:W)==P`, the login persists
- **And** `pool.Close` was called **zero** times during the reload
- **And** exactly **one** register/release cycle occurred for `ws:W`, not two

**Scenario: the panel names the real reason — US-14/AC2+AC3, FR-008a**
- **Given** a custom browser-allowed agent on no workspace
- **When** the live panel attaches with that `agent_id`
- **Then** the rendered reason says the agent is not on a workspace
- **And** it is **not** the string "browser tools may not be registered for this agent"
- **And given** the pool is full instead, **then** the reason names the cap; **and given** two candidate workspaces, **then** it names the ambiguity

**Scenario: the wire meaning change is caught, not smuggled — US-10, FR-016, FR-029**
- **Given** the implementation is complete
- **When** `make gen-contracts` then `make verify-contracts` run
- **Then** exit 0, and the `contracts/` diff touches only `description:` text in the three browser schemas
- **And** the two reversed descriptions match FR-016's verbatim replacement text
- **And** a client sending an attach frame whose `session_id` belongs to workspace B's chat, with `agent_id` on both A and B, resolves to **B** — proving `session_id` is now binding

**Scenario: audit answers "who acted as the signed-in user" — US-13, FR-027, FR-035**
- **Given** Mia establishes W's browser and Jim later acts in it
- **Then** one audit event records the creation (key, workspace, agent=mia)
- **And** one records Jim's first use of a browser he did not establish
- **And** every `pool.Acquire` call in the run carried a key returned by `ResolveBrowsingKey` in the same turn

---

## 9. Traceability matrix (FR ↔ US ↔ BDD ↔ test ↔ ADR/review)

| FR | Requirement | US | BDD | Test (TDD) | Source |
|---|---|---|---|---|---|
| FR-001 | One `BrowserManager` per browsing key; `browserMgrs` re-keyed | US-1 | handover | `TestLoop_BrowserManagerForKey_OnePerKey` | D1.1(1) |
| FR-002 | Every browser tool addresses the resolved key, not `DefaultSessionID` | US-1 | handover | `TestTools_UseResolvedKeyNotConstant` | D1.1(2) |
| **FR-002a** | **No tool holds a `*BrowserManager` captured at registration; every tool resolves its manager per `Execute` via `ManagerResolver`** | US-1/AC3 | two-agents-real-registration | `TestRegisterTools_NoBoundManagerField`, `TestHandover_ThroughRealRegistrationPath` | CRIT-002 |
| **FR-002b** | Every one of the 37 `DefaultSessionID` consumers (§2.2) addresses the resolved key; the constant and its alias are **deleted** | US-1, US-9 | human-outranks-lease | `TestNoResidualDefaultSessionID` (repo-wide structural) | CRIT-005 |
| **FR-002c** | `controlledResult` resolves the control lock against the browsing key | US-9/AC2 | human-outranks-lease | `TestControlledResult_UsesResolvedKey` + `tools_control_test.go` re-run | CRIT-005 |
| **FR-002d** | `loop.go:270-279`'s standing "do NOT reintroduce a single shared field" comment is **replaced**, not deleted: the map stays a map, keyed by browsing key, and the comment says why | US-1 | — | `TestLoop_BrowserMgrsCommentIsCurrent` (doc-comment assertion) | MIN-008 |
| FR-003 | Cross-workspace cookie/storage isolation, by Chrome profile | US-3 | cross-workspace-isolation | `TestBrowsingContext_CrossWorkspaceIsolation` | D1.1a / ADR crit 5b |
| FR-004 | Login in X invisible in Y | US-3 | cross-workspace-isolation | same | ADR crit 5b |
| FR-005 | New chat in same workspace stays logged in | US-4 | new-chat-same-workspace | `TestBrowsingContext_NewChatSameWorkspaceSamePID` | ADR crit 5c |
| FR-006 | Agent switch requires no handover step | US-1 | handover | `TestHandover_NoCommandRequired` | ADR crit 2 |
| FR-007 | Resolution ladder: workspace ctx → unambiguous `FindForAgentPreferring` → fail | US-6 | scheduled-turn-resolves | `TestResolveBrowsingKey_Ladder` | D1.4 |
| FR-008 | No workspace ⇒ `ErrNoBrowsingContext`, never a shared browser | US-6/AC2 | workspace-less-refused | `TestResolveBrowsingKey_NoWorkspaceFailsByName` | D1.4 |
| **FR-008a** | The gateway/panel failure reason distinguishes no-workspace / ambiguous / pool-full / not-registered (`BrowserResolveOutcome`) | US-14 | panel-names-real-reason | `TestGateway_ResolveOutcomes_AreDistinct` | MAJ-003 (residual) |
| **FR-009** | **A delegated sub-turn uses its workspace's browser and its logins — no separate key, manager, Chrome or profile** | US-5/AC1+AC2 | delegated-shares-browser | `TestSubTurn_UsesWorkspaceBrowser` | D1.2 (superseding ruling) |
| FR-010 | `BrowserManager.Viewers() int` accessor, consumed by the reaper and the pool's idle-close | US-12 | idle-close, viewer-pin | `TestManager_Viewers_ReflectsAttachDetach` | §12 A3 |
| ~~FR-011~~ | **WITHDRAWN.** Per-key browser-context map. The D1.2 ruling removed the second key shape; `browserCtxID` stays a single field and is retired entirely by FR-031. No behaviour is specified. | — | — | — | D1.2 |
| ~~FR-012~~ | **WITHDRAWN.** Unattended login-wall failure text. There is no unattended jar to fail against. | — | — | — | D1.2 |
| FR-013 | `ListTabsState` returns a closed 3-value state | US-7 | three-tab-states | `TestListTabsState_ThreeDistinctStates` | D1.5 |
| FR-014 | A denied agent's answer is a policy denial, never a tab payload | US-8/AC1 | denied-agent | `TestListTabs_DeniedAgentNeverReachesTool` | D1.5 |
| **FR-014a** | The denial for any `browser_*` tool names the browser surface, not the generic `"Tool execution denied by policy."` | US-8/AC2 | denied-agent | `TestToolDenial_BrowserSurfaceIsNamed` | MAJ-005 / ADR crit 3b |
| FR-015 | The 5 model-visible "shared browser session" strings are corrected | US-1 | — | `TestToolDescriptions_NoFalseSharedClaim` | D1.3 |
| **FR-034** | The replacement description literals are **specified here**, and are accurate for the one key shape that exists | US-1 | — | same test, asserting the new literal | MIN-005 |
| FR-016 | Gateway resolves agent→workspace server-side; no wire field added; the two reversed descriptions use FR-016's verbatim text | US-10 | wire-meaning-change-caught | `TestGateway_SessionIDIsBinding` | ADR-043 D3 / MAJ-004 |
| **FR-016a** | The capture registry is keyed by browsing key; **one capture session per workspace browser**; ADR-048's "requesting agent" conflict rule collapses | US-2 | human-browses-first | `TestCaptureRegistry_OnePerBrowsingKey` | MAJ-007 |
| **FR-016b** | Boot warm-tab warms the resolved workspace of the default agent; skipped with one INFO (not WARN) when nothing resolves | — | — | `TestPickWarmBrowser_UsesResolvedKey` | MAJ-006 |
| FR-017 | Gateway prefers the attaching session's `workspace_id` | US-2, US-11 | human-browses-first | `TestGateway_PrefersSessionWorkspaceID` | round-1 C4 |
| FR-018 | Multi-workspace agent: turn and panel agree, including agreeing to refuse | US-11 | ambiguous-refused | `TestMultiWorkspaceAgent_TurnAndPanelAgree` | §6 Q2 |
| FR-019 | Per-browser write lease held for one action-tool call (**§14**) | US-9 | two-writers | `TestWriteLease_OneWriterPerBrowser` | D2.10 |
| **FR-019a** | **Every** mutating `browser_*` tool acquires the lease; the exempt set is a closed list of **five** (3 read-only + `browser_snapshot` + `browser_handle_dialog`); the check is registry-driven | US-9/AC5 | every-mutating-tool-leased | `TestWriteLease_EveryActionToolIsLeased` | MAJ-008, D2 §15.2 |
| FR-020 | Loser gets non-error `{"deferred":true,"reason":…}` | US-9 | two-writers | `TestWriteLease_LoserGetsDeferredNotError` | D2.10 |
| FR-021 | Read-only tools ungated | US-9/AC4 | read-only-never-deferred | `TestWriteLease_ReadOnlyToolsUngated` | D2.10 |
| FR-022 | `controlledResult` evaluated before the lease | US-9/AC2 | human-outranks-lease | `TestWriteLease_HumanControlTakesPrecedence` | ADR-038 D6 |
| FR-023 | Bounded wait before declaring contention; `leaseWaitTimeout` and its clock seam named in §14 | US-9 | two-writers | `TestWriteLease_BoundedWait` | §14 / MIN-007 |
| FR-024 | Lease always released on panic/cancel/timeout | US-9/AC3 | panic-does-not-wedge | `TestWriteLease_ReleasedOnPanicAndCancel` | D2.10 |
| FR-025 | Per-tab reaping semantics asserted, not rewritten; viewer pins the browser | US-12/AC1+AC2 | tabs-reap-viewer-pins | `TestReap_PerTabTTLAndViewerPin` | round-1 M4 |
| FR-026 | Disposal on workspace deletion / roster removal | US-12/AC3 | disposal-on-workspace-deletion | `TestDispose_OnWorkspaceDeletion` | §6 Q4/Q5 |
| **FR-026a** | The reload prune's liveness predicate is the set of **live browsing keys** — a workspace key is live while the workspace exists and has ≥1 browser-policy-allowed agent on its CoreTeam. It is **never** `registry.ListAgentIDs()` | US-17/AC1 | reload-preserves-login | `TestReload_PruneUsesBrowsingKeys` | CRIT-003 |
| **FR-026b** | Registration is idempotent per key: N agents on one workspace produce exactly one register/release pair per reload | US-17/AC2 | reload-preserves-login | `TestReload_OneCyclePerKeyNotPerAgent` | CRIT-003 |
| **FR-026c** | A delegated sub-turn creates no browser of its own, so K sub-turns return the pool and manager counts to baseline | US-12/AC5 | k-subturns-leak-nothing | `TestSubTurns_NoPoolGrowth` | CRIT-006 (as re-scoped by D1.2) |
| FR-027 | Audit on browser creation and first cross-agent use | US-13 | audit-repudiation | `TestAudit_CreateAndFirstCrossAgentUse` | D2.11 |
| FR-028 | Reload preserves pid + profile + login | US-17 | reload-preserves-login | `TestReload_PreservesPIDAndLogin` | ADR-043 CRIT-002, re-mechanised |
| FR-029 | `make verify-contracts` green; prose-only schema diff | US-10/AC1 | wire-meaning-change-caught | `make verify-contracts` | Hard Constraint #8 |
| FR-030 | No new platform-conditional **behaviour**; the lease is in-process `sync`, never `fileutil.WithFlock` | US-9 | — | `TestLease_IsInProcessOnly_NoFlock` | §6 platform |
| **FR-031** | `tools.browser.capture_shared_context` is **retired**, with `Register`'s CDP-context branch, `disposeBrowserContextRaw`, `contextCount()`, `SetCaptureSharedContext`, `CaptureSharedContextEnabled` and the stale ADR-061 JPEG reference in its doc comment | US-3/AC2 | cross-workspace-isolation | `TestNoCDPBrowserContextIsEverCreated` | D1.0a / D1.1a |
| **FR-032** | An `ask`-policy tool reached from a delegated sub-turn is **auto-denied**, never queued. #659 is a prerequisite | US-5/AC3 | subturn-ask-denied | `TestSubTurn_AskPolicyIsAutoDenied` | MAJ-010 / ADR D2.9 |
| **FR-033** | Ambiguous multi-workspace resolution **refuses** for a browsing key; the WARN fires on the preferring path as well as the plain one | US-6/AC3, US-11/AC2 | ambiguous-refused | `TestResolveBrowsingKey_AmbiguousRefuses` | MAJ-011 |
| **FR-035** | Every `pool.Acquire` in a run carried a key returned by `ResolveBrowsingKey` in the same turn (behavioural, not just structural) | US-13 | audit-repudiation | `TestAcquire_KeyProvenance` | MIN-009 |
| **FR-036** | Cancelling a parent turn cancels its delegated sub-turns' in-flight browser work via the inherited `routingSessionID`; no browser is closed by the cancel (the browser belongs to the workspace, not the turn) | US-5 | — | `TestCancel_CascadesWithoutClosingBrowser` | §6 Q6 |
| **FR-037** | One Chrome process and one `--user-data-dir` profile directory per browsing key, via `pipeLaunchConfig.userDataDir` | US-3 | cross-workspace-isolation | `TestPool_OneChromePerKey` | D1.1a |
| **FR-038** | A configurable cap, `tools.browser.max_browsers`, on concurrently live Chromes | US-15 | pool-refuses-at-cap | `TestPool_CapIsEnforced` | D1.1a item 1 |
| **FR-039** | At the cap the pool **refuses** with `ErrBrowserPoolFull`; it never evicts a live browser | US-15/AC1+AC2 | pool-refuses-at-cap | `TestPool_RefusesNeverEvicts` | D1.1a item 2 |
| **FR-040** | Whole-Chrome idle close: zero tabs and zero viewers past the idle window closes the process; the **profile directory survives** | US-12/AC4 | idle-close-keeps-profile | `TestPool_IdleCloseKeepsProfile` | D1.1a item 3 |
| **FR-041** | Crash containment: one Chrome's death affects exactly one key; other keys' managers are not reset; recovery relaunches from the profile so the login survives | US-16 | one-crash-one-workspace | `TestPool_CrashIsContained` | D1.1a item 4 |
| **FR-042** | Per-key launch lock and ownership marker (`<ProfileDir>/ws/<id>/chrome.lock`, `$OMNIPUS_HOME/browser/ws-<id>.pid`) replacing the singletons at `coordinator.go:1424,1527` | US-15 | — | `TestPool_PerKeyLockAndMarker` | D1.1a |
| **FR-043** | Reload survival is by **profile on disk**, replacing ADR-043 CRIT-002's context re-adoption | US-17/AC1 | reload-preserves-login | `TestReload_PreservesPIDAndLogin` | D1.1a |
| **FR-044** | **Gate G-1.** `max_browsers`' shipped default is set from a recorded measurement of marginal per-Chrome RSS, not an estimate | US-15/AC3 | — | measurement recorded in the PR body | §0.3 |
| **FR-045** | **Gate G-2.** Before the pool is built, a spike proves `chrome.tabCapture` succeeds for a tab in a **second Chrome's default context** with its own `--user-data-dir` | US-16 | — | `TestSpike_CaptureAgainstSecondChrome` (real Chrome, `skipIfNoBrowser`) | §0.3 |

**Withdrawn rows are kept, not renumbered,** so that a reader arriving from the round-2 review can see that FR-011/FR-012 were deleted by ruling rather than lost in an edit. They carry no design content.

**Traceability completeness (the round-2 structural PARTIALs, closed).** Every US has ≥1 BDD scenario; every AC in §7 is reachable from one; every BDD scenario names its US/AC and FRs inline and has a §10 row. **Nine FRs deliberately carry no BDD scenario**, and each is a structural, build-time or measurement requirement rather than an observable behaviour — a Given/When/Then for them would be theatre:

| FR | Why no BDD | How it is verified instead |
|---|---|---|
| FR-002d | A doc comment | Test 9-style doc assertion (`TestLoop_BrowserMgrsCommentIsCurrent`) |
| FR-016b | Boot-time best-effort warm-up, invisible to any user-facing flow | Test 24 |
| FR-019a | A structural rule over the registry | Test 18 (registry-driven) |
| FR-029 | A build gate | `make verify-contracts` |
| FR-030 | A structural import assertion | Test 19 |
| FR-035 | A whole-run provenance invariant, not a single interaction | Test 34 |
| FR-036 | Cancellation cascade, covered as a unit-level property | Test 36 |
| FR-042 | Path construction | Test 27 |
| FR-044, FR-045 | Measurements (gates G-1, G-2), not behaviours | Recorded in the PR body; test 37 for G-2 |

Everything else in §9 has all four of US, BDD, test and source.

---

## 10. TDD plan (ordered; Unit → Integration → E2E)

| # | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 1 | `TestResolveBrowsingKey_Ladder` | Unit | FR-007 | Table-driven over three rungs. **Write first** |
| 2 | `TestResolveBrowsingKey_NoWorkspaceFailsByName` | Unit | FR-008 | `errors.Is(err, ErrNoBrowsingContext)` **and** key `IsZero()` |
| 3 | `TestResolveBrowsingKey_AmbiguousRefuses` | Unit | FR-033 | Two candidate workspaces, no preference → refusal, both named, WARN on both paths |
| 4 | `TestRegisterTools_NoBoundManagerField` | Unit (structural) | FR-002a | Reflects over every exported tool struct in `pkg/tools/browser`; fails on any `*BrowserManager` field. **Red today** |
| 5 | `TestNoResidualDefaultSessionID` | Unit (structural) | FR-002b | **Repository-wide**, non-test: zero references to `DefaultSessionID`/`defaultSessionID`. Baseline today: 37 executable + 12 comment |
| 6 | `TestControlledResult_UsesResolvedKey` | Unit | FR-002c | Control lock taken under `ws:W`; `controlledResult` must see it. **Red today** (it asks `IsControlled("default")`) |
| 7 | `TestLoop_BrowserManagerForKey_OnePerKey` | Unit | FR-001 | Concurrent callers for one key get one manager; different keys, different managers |
| 8 | `TestListTabsState_ThreeDistinctStates` | Unit | FR-013 | All three states constructed directly; pairwise-distinct payloads; state set is exactly `{no_context, open, empty}` |
| 9 | `TestToolDescriptions_NoFalseSharedClaim` | Unit | FR-015, FR-034 | Asserts the old phrase is gone **and** the new literal is present, verbatim from FR-034 |
| 10 | `TestToolDenial_BrowserSurfaceIsNamed` | Unit | FR-014a | The `browser_*` denial `ModelMessage` names the browser and differs from the generic string |
| 11 | `TestListTabs_DeniedAgentNeverReachesTool` | Unit | FR-014 | Policy-filtered registry; `Execute` not entered |
| 12–17 | `TestWriteLease_*` (OneWriterPerBrowser, LoserGetsDeferredNotError, ReadOnlyToolsUngated, HumanControlTakesPrecedence, ReleasedOnPanicAndCancel, BoundedWait) | Unit | FR-019…FR-024 | Per **§14**; fake clock via §14's named seam |
| 18 | `TestWriteLease_EveryActionToolIsLeased` | Unit (structural) | FR-019a | **Registry-driven, not list-driven** — enumerates registered `browser_*` tools and checks each against the closed exempt set |
| 19 | `TestLease_IsInProcessOnly_NoFlock` | Unit (structural) | FR-030 | `lease.go` imports no `fileutil`/`unix` locking |
| 20 | `TestManager_Viewers_ReflectsAttachDetach` | Unit | FR-010 | The accessor the reaper and idle-close consume |
| 21 | `TestGateway_ResolveOutcomes_AreDistinct` | Unit | FR-008a | All four `BrowserResolveOutcome` values produce different operator-facing reasons |
| 22 | `TestGateway_PrefersSessionWorkspaceID` | Unit | FR-017 | Session meta `workspace_id=B` beats the plain ladder |
| 23 | `TestMultiWorkspaceAgent_TurnAndPanelAgree` | Unit | FR-018 | Including agreeing to refuse |
| 24 | `TestPickWarmBrowser_UsesResolvedKey` | Unit | FR-016b | Warmed session id equals the resolved key; no-workspace ⇒ skipped with INFO |
| 25 | `TestCaptureRegistry_OnePerBrowsingKey` | Unit | FR-016a | Two agents, one workspace ⇒ one capture session |
| 26 | `TestPool_CapIsEnforced` / `TestPool_RefusesNeverEvicts` | Unit (fake launcher) | FR-038, FR-039 | Uses the existing injectable `pipeLauncher` seam (`coordinator.go:149`) — no real Chrome |
| 27 | `TestPool_PerKeyLockAndMarker` | Unit | FR-042 | Distinct lock and marker paths per key |
| 28 | `TestPool_CrashIsContained` | Unit (fake launcher) | FR-041 | Kill one instance; assert the other's manager was not reset |
| 29 | `TestSubTurn_UsesWorkspaceBrowser` | Unit | FR-009 | Sub-turn ctx resolves to `ws:W`; no second manager |
| 30 | `TestSubTurn_AskPolicyIsAutoDenied` | Unit | FR-032 | Denied, not queued; no approval request created |
| 31 | `TestSubTurns_NoPoolGrowth` | Unit | FR-026c | K sub-turns; counts return to baseline |
| 32 | `TestReload_PruneUsesBrowsingKeys` | Unit | FR-026a | Prune with a workspace-keyed map disposes **nothing**. **Red today** |
| 33 | `TestReload_OneCyclePerKeyNotPerAgent` | Unit | FR-026b | **Two agents on one workspace**; exactly one register/release pair |
| 34 | `TestAcquire_KeyProvenance` | Unit | FR-035 | Every `Acquire` key traced to a `ResolveBrowsingKey` return in the same turn |
| 35 | `TestNoCDPBrowserContextIsEverCreated` | Unit (structural + behavioural) | FR-031 | No call to `target.CreateBrowserContext`/`WithNewBrowserContext` anywhere non-test |
| 36 | `TestCancel_CascadesWithoutClosingBrowser` | Unit | FR-036 | Parent cancel stops the sub-turn's work; `LiveKeys()` unchanged |
| 37 | **`TestSpike_CaptureAgainstSecondChrome`** | **Spike (real Chrome)** | FR-045 | **Gate G-2. Run before Stream P starts.** Two Chromes, distinct `--user-data-dir`s, capture a tab in the second |
| 38 | `TestHandover_ThroughRealRegistrationPath` | Integration (real Chrome) | FR-002a, FR-006 | **The headline defect.** Two agents registered via `registerSharedTools`, one workspace, one tab opened by A and listed by B. **Red today** |
| 39 | `TestBrowsingContext_CrossWorkspaceIsolation` | Integration (real Chrome) | FR-003, FR-004, FR-037 | Cookie set in X, absent in Y; distinct pids and profile dirs |
| 40 | `TestBrowsingContext_NewChatSameWorkspaceSamePID` | Integration | FR-005 | What the workspace axis buys over the conversation axis |
| 41 | `TestPool_IdleCloseKeepsProfile` | Integration | FR-040 | Process gone, profile dir present, login survives the next launch |
| 42 | `TestReload_PreservesPIDAndLogin` | Integration | FR-028, FR-043 | Two agents on W; pid unchanged; `pool.Close` called **zero** times |
| 43 | `TestReap_PerTabTTLAndViewerPin` | Integration | FR-025 | Existing semantics survive the re-key |
| 44 | `TestDispose_OnWorkspaceDeletion` | Integration | FR-026 | Counts `pool.Close` calls |
| 45 | `TestAudit_CreateAndFirstCrossAgentUse` | Integration | FR-027 | Two events, correct fields |
| 46 | `TestGateway_SessionIDIsBinding` | Integration | FR-016 | Attach frame's `session_id` selects the workspace — the assertion that makes the contract gate falsifiable |
| 47 | `TestWriteLease_TwoAgentsRealChrome` | E2E | FR-019, FR-020 | Real navigations, no interleaved DOM |
| 48 | `TestPool_CrashContainment_RealChrome` | E2E | FR-041 | Kill one Chrome; the other workspace's panel keeps streaming |
| 49 | `make verify-contracts` | Build | FR-029 | Exit 0 |

### 10.1 Regression requirements (MANDATORY — this change modifies shipped behaviour)

**Must keep passing, unmodified:**

- `pkg/tools/browser/tab_adoption_e2e_test.go` — all **nine** ADR-041 tab tests (`:77`–`:569`). Tab-set semantics inside one browser are untouched.
- `pkg/tools/browser/shared_control_test.go` — **eight** tests (`:35, :55, :74, :92, :109, :138, :157, :186`), not nine as the previous draft claimed. **Correction that matters more than the count:** *this file is not the FR-022 guard.* None of its eight tests calls `controlledResult`; they exercise `LiveView` input dispatch, rate limiting and viewport failure streaks. A green result here proves nothing about the human control lock surviving the re-key.
- **`pkg/tools/browser/tools_control_test.go` — the actual `controlledResult` guard, and absent from the previous draft entirely.** Three tests: `TestExecute_ControlLock_InteractiveToolsDeferWhileControlled` (`:59`), `TestExecute_ControlLock_ReadOnlyToolsAreNotGated` (`:106`), `TestExecute_ControlLock_ReleaseUngatesInteractiveTools` (`:153`). These **must be re-run against the re-keyed lock** and must pass without weakening any assertion. If they need editing, FR-002c is wrong.
- `pkg/tools/browser/idle_reaper_test.go`, `reaper_edge_test.go`, `reaper_lifecycle_test.go` — FR-025 asserts per-tab reaping is not rewritten. (Whole-Chrome close is *new* behaviour tested separately, not a change to these.)
- `pkg/tools/browser/coordinator_test.go` — `TestCoordinator_OwnershipMarker_RoundTrip` (`:379`), `TestCoordinator_UnlimitedDefault_AllowsPastOldCap` (`:429`), `TestCoordinator_PositiveCap_StillRejectsAtBoundary` (`:448`), `TestCoordinator_ConcurrentOpeners_PositiveCap_ExactlyOneWinner` (`:472`), `TestCoordinator_SetMaxTotalTabs_ReloadRestoresUnlimited` (`:506`). The **tab** budget is orthogonal to the **browser** cap and must not regress.

**Must be rewritten, not extended:**

- `pkg/tools/browser/coordinator_test.go:154` `TestCoordinator_TwoAgents_OneChrome_TwoContexts` → `TestPool_TwoWorkspaces_TwoChromes`. Its per-agent assertion is now the wrong assertion, and its *"one Chrome"* premise is exactly what D1.1a replaces. Leaving it green while the model changed underneath is the `docs/internal/false-green-patterns.md` stale-green shape.
- `pkg/tools/browser/coordinator_test.go:203` `TestManager_Shutdown_DropsConnectionNotProcess` and `:244` `TestCoordinator_Shutdown_IsSoleKill` → re-scope to *"…for the key's own Chrome"*. Both encode the single-process model.
- `pkg/tools/browser/stress_5agents_test.go:267` `TestFiveAgents_ConcurrentStress` → **five agents on one workspace** (contention — the new normal case) **plus** five agents across five workspaces (isolation, bounded by the cap). Five agents on five implicit per-agent jars is no longer a scenario the product has.

**Must be DELETED, and this is a finding rather than a rename:**

- `pkg/tools/browser/coordinator_test.go:328` `TestCoordinator_Register_SharedContextMode_ReturnsRootCtxAndEmptyBrowserCtxID`. It asserts that `Register` returns an **empty** `browserCtxID` and that `coord.contextCount() == 0` — i.e. **it is the test that proves the isolation this spec promises did not exist.** The previous draft asked only to "re-key its Register argument", which would have left a green test asserting the absence of the product's headline guarantee. Under FR-031 the mode, the flag and `contextCount()` are all retired, so the test has nothing left to assert and is deleted with them. Its replacement is test 35, `TestNoCDPBrowserContextIsEverCreated`, which asserts the opposite property: no CDP browser context is created **anywhere**, because isolation is now the profile directory.

**Verification receipt discipline:** run each scoped test as `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/tools/browser/ > log 2>&1; echo "exit=$?"` — never through a pipe (a piped `tail` reports tail's status). Do not run the full suite locally; push and read CI.

### 10.2 Test datasets

| Input | Expected | Traces to |
|---|---|---|
| ctx: `workspace_id=W` | key `ws:W` | FR-007 |
| ctx: `workspace_id=""`, agent on exactly one CoreTeam (W) | key `ws:W` | FR-007 |
| ctx: `workspace_id=""`, agent on **no** CoreTeam | `ErrNoBrowsingContext`, zero key | FR-008 |
| ctx: `workspace_id=""`, agent on **two** CoreTeams (A, B) | `ErrNoBrowsingContext` naming A and B; WARN on both resolution paths | FR-033 |
| ctx: sub-turn, `workspace_id=W` inherited (`subturn.go:1323`) | key `ws:W`; no new manager, no new Chrome | FR-009, FR-026c |
| ctx: sub-turn, tool policy `ask`, no operator | denied with the headless reason; no approval request | FR-032 |
| agent on A and B; session meta `workspace_id=B` | `ws:B` from **both** turn and gateway | FR-018 |
| two agents, one workspace, real `registerSharedTools` | one `*BrowserManager` pointer for both | FR-002a |
| manager with no `sessions` entry for its key | `TabStateNoContext`, empty tabs | FR-013 |
| manager, browser live, 2 tabs | `TabStateOpen`, 2 tabs | FR-013 |
| manager, browser live, `len(se.tabs)==0` | `TabStateEmpty`, empty tabs | FR-013 |
| `mia` calls `browser_list_tabs` | denial naming the browser surface; `Execute` not entered | FR-014, FR-014a |
| 2 concurrent `browser_navigate` on one key | 1 executes, 1 `deferred:true`, 0 errors | FR-019, FR-020 |
| 8 concurrent action tools on one key | 1 at a time; 7 deferred or waited; 0 errors; 0 deadlocks | FR-019, FR-023 |
| lease holder panics mid-action | next acquire succeeds ≤ `leaseWaitTimeout` | FR-024 |
| human holds control lock (key `ws:W`) + agent action | ADR-038 D6 reason; lease never acquired | FR-002c, FR-022 |
| `max_browsers=2`, 2 live, request for a 3rd workspace | `ErrBrowserPoolFull`; both live pids unchanged | FR-038, FR-039 |
| W's browser: 0 tabs, 0 viewers, past idle window | process closed; `LiveKeys()` shrinks; profile dir present | FR-040 |
| W1's Chrome killed, W2 live with a viewer | W2 pid/tabs/stream unaffected; W2's manager not reset | FR-041 |
| W1 relaunched after kill | logged in (profile survived) | FR-041, FR-043 |
| reload, 2 agents on W, live login | pid unchanged; `pool.Close` count 0; 1 register/release pair | FR-026a, FR-026b, FR-028 |
| workspace W deleted with a live browser | exactly one `pool.Close(ws:W)` | FR-026 |
| K sub-turns run to completion | pool and manager counts equal baseline | FR-026c |
| attach frame `session_id` from B's chat, agent on A and B | resolves to `ws:B` | FR-016, FR-017 |

---

## 11. Functional requirements & success criteria

- **FR-001 … FR-045** as enumerated in §9 (FR-011 and FR-012 withdrawn). All MUST.

Every criterion below states **what would make it fail.** The round-2 review found four gates that could not fail for the defect they named; each is rewritten here and its failure mode is spelled out.

- **SC-001 (headline, the reported defect).** Browse as Mia in workspace W; switch the chat to Jim; Jim's `browser_list_tabs` returns the tab. Measured by test 38, `TestHandover_ThroughRealRegistrationPath`, which goes through `registerSharedTools` — **not** a hand-built manager. *Fails if:* the two agents resolve different managers. **This test is red today**; if it is green before the change, it is not exercising the real path.
- **SC-002 (isolation exists and is by profile).** `TestBrowsingContext_CrossWorkspaceIsolation` passes against real Chrome, asserting a missing cookie **and** distinct pids **and** distinct `--user-data-dir` paths, under the **shipped default configuration** — no flag flip, no env var. *Fails if:* the cookie is present, the pids match, or the test needs a non-default config to pass. **The previous SC-002 could only pass with `capture_shared_context=false`, i.e. it proved a property of a configuration nobody ships.** FR-031 removes the flag so this criterion has no configuration to hide behind.
- **SC-003 (no silent merge, behavioural not just structural).** Zero `pool.Acquire` calls in a full run carried a key that did not come from a `ResolveBrowsingKey` return in the same turn (test 34). *Fails if:* any acquire's key is untraceable. The old SC-003 asserted only that `BrowsingKey`'s field was unexported — which constrains key *construction*, never *use*, and would not have caught a parent's key being passed inside a sub-turn.
- **SC-004 (delegated work shares, and leaks nothing).** `TestSubTurn_UsesWorkspaceBrowser` asserts the sub-turn is logged in on `ws:W`, and `TestSubTurns_NoPoolGrowth` asserts K sub-turns return the pool and manager counts to baseline. *Fails if:* a sub-turn gets its own key, or the counts grow. **This inverts the previous SC-004** (which asserted a distinct context for the sub-turn) because the ruling inverted the requirement — recorded so the reversal is visible rather than looking like a deleted test.
- **SC-005 (concurrency is deterministic).** Eight concurrent action tools on one workspace browser, repeated 50× under `-race`: zero errors, zero deadlocks, exactly one executing writer at any instant. *Fails if:* any interleaving, error, or hang.
- **SC-006 (three states).** A table-driven test enumerates all three `ListTabsState` values and asserts pairwise-distinct model-visible payloads; a fourth value is a compile-time impossibility. *Fails if:* any two payloads are equal, or a new value is added without updating the test.
- **SC-007 (contract intact, including its meaning).** Three conditions, all required. (1) `make verify-contracts` exits 0. (2) The `contracts/` diff contains no `properties:`, `required:`, `enum:` or `type:` change. (3) **`TestGateway_SessionIDIsBinding` passes** — an attach frame whose `session_id` belongs to workspace B's chat resolves to B for an agent on both A and B. *Fails if:* the resolution ignores `session_id`. **Condition (3) is the fix for the round-2 finding that this gate could not fail:** conditions (1) and (2) are shape checks, and the change FR-016 makes is a *semantic reversal* of a documented guarantee (`BrowserAttachFrame.yaml`: the server binds *"regardless of the value sent here"*). A shape check passes a reversal cleanly. Additionally, the two replacement description strings must be reviewed against FR-016's verbatim text, and `BrowserInspectRequest` must be confirmed **not** to have gained chat-session semantics (US-10/AC3).
- **SC-008 (nothing green by accident).** Every rewritten test in §10.1 is confirmed to **fail** against the pre-change code and **pass** after. **Extended to the four tests the round-2 review identified as unfalsifiable**, each of which must be red today: test 4 (`TestRegisterTools_NoBoundManagerField`), test 6 (`TestControlledResult_UsesResolvedKey`), test 32 (`TestReload_PruneUsesBrowsingKeys`), test 38 (`TestHandover_ThroughRealRegistrationPath`). A test that passes both ways is not evidence.
- **SC-009 (the control lock is provably alive).** `tools_control_test.go`'s three tests pass against the **re-keyed** lock, with no assertion weakened. *Fails if:* any assertion is relaxed to accommodate the re-key. **`shared_control_test.go` is explicitly not this criterion's guard** — it never calls `controlledResult`.
- **SC-010 (the pool is bounded and honest).** With `max_browsers = N`: `len(pool.LiveKeys()) <= N` at every instant of a run that requests N+3 workspaces; the three refusals carry `ErrBrowserPoolFull`; and no live browser's pid changed. *Fails if:* the pool ever exceeds N, or a refusal is served by closing something.
- **SC-011 (a crash is contained).** Killing one workspace's Chrome leaves every other workspace's pid, tab set, viewer stream and cookies intact, and does not reset their managers. *Fails if:* any other key is affected — which is today's behaviour (`watchForCrash` resets every connector manager).
- **SC-012 (measurements exist).** G-1's per-Chrome marginal RSS figure and G-2's capture spike result are **recorded in the implementing PR body** before `max_browsers`' default is set or Stream P merges. *Fails if:* the default is a round number with no measurement behind it.
- **SC-013 (no residual constant).** Repo-wide, non-test references to `DefaultSessionID`/`defaultSessionID` are **zero**, down from 37 executable + 12 comment. *Fails if:* any survives — each one is a surface still addressing `"default"` while the rest of the system addresses `ws:<id>`.
- **SC-014 (gates).** `gofmt -l . | wc -l` = 0; `golangci-lint run --build-tags=goolm,stdjson` exit 0; CI `go test -tags goolm,stdjson -count=1 ./...` exit 0; `govulncheck` 0 vulnerabilities; `npm run typecheck` exit 0; `npx vitest run` exit 0.
- **SC-015 (attribution, blocking).** §12 A10 — the operator of record for the 2026-08-31 rulings — is resolved in **ADR-072** before implementation starts. **This is a prerequisite, not a footnote.** The ADR now names Daniel Piatkowski for the D1.1a and D1.2 rulings; the remaining D1 rulings (isolation axis, no-fallback, browser seed, lease ownership) must carry the same attribution. *Fails if:* any D1 decision still reads "operator ruling, 2026-08-31" with no named decider.

---

## 12. Ambiguity self-audit

Each item is **resolved here as a recorded assumption** unless marked otherwise; a different ruling changes the spec.

| # | Ambiguity | Resolution |
|---|---|---|
| **A1** | **Does a key change alone make a browser isolated?** No. Under the previous CDP-context design it did not (a second `sessions` entry reused one `browserCtxID`), and under the current design it does not either — the isolation is the **profile directory**, which only exists because a separate Chrome was launched with a separate `--user-data-dir`. | **DECIDED (FR-037):** isolation is a property of `pipeLaunchConfig.userDataDir` (`exec_resolver.go:385`), one per key. A test that asserts only distinct map keys proves nothing; every isolation test asserts distinct **pids and profile paths** (§10.2). |
| **A2** | **~~Discriminator for "unattended"~~** | **WITHDRAWN by the D1.2 ruling.** No discriminator is built. `spawnSubTurn`'s `WorkspaceID: parentTS.opts.WorkspaceID` (`subturn.go:1323`) is correct as-is. Recorded so a reader of the previous draft does not implement it. |
| **A3** | **~~Viewer-count attendance seam~~** | **PARTIALLY WITHDRAWN.** Attendance is not a concept any more. `BrowserManager.Viewers()` is still added (FR-010) because the **reaper's viewer pin** and the **pool's idle-close** both need it and no exported accessor exists today. |
| **A4** | **D1.5's third state cannot be a `ListTabs` return value.** "Not permitted" is produced by the policy layer (`tool_denial.go:206-210`); a tool policy stopped from running cannot report why. | **DECIDED (FR-013/FR-014/FR-014a):** `TabState` is a closed three-value enum with **no** "denied" member; the third state is an end-to-end observable, and FR-014a strengthens the artefact so it is at least *testable at the tool boundary* rather than only in a transcript. |
| **A5** | **`TabStateEmpty` reachability.** `tabs.go:50-52` asserts a running browser with zero tabs "cannot occur", yet `ReapIdleSessions` has a real zero-tab branch and `CloseTab` can empty `se.tabs`. | **DECIDED:** `TabStateEmpty` is **reachable but transient**, and is specced (US-7/AC3). Resolution failure is **not** a `TabState` — it is an error (FR-008), because an error is the only shape a model reliably treats as "stop and report". |
| **A6** | **"No wire change" is true of the schema and false of the meaning.** `BrowserAttachFrame.yaml` says the server binds *"regardless of the value sent here"* and *"agent_id is the binding key"*. FR-017 makes `session_id` binding. | **DECIDED (FR-016, SC-007):** state the reversal plainly, quote the replacement text, and add a **behavioural** assertion (test 46) so the gate can fail. `BrowserInspectRequest.session_id` is a *browser* session id, not a chat session id, so it does **not** gain the semantics; `browser_inspect` resolves from the agent alone under FR-033. **If implementation finds resolution needs a field the frames do not carry, STOP** — that is Hard Constraint #8's 5-step process and a spec amendment, not a code change. |
| **A7** | **The write lease is filed under D2.10 but ADR §4 calls it the largest open risk in D1, and both specs specced it with incompatible APIs.** | **DECIDED (operator ruling):** **this spec owns it — §14 is the single normative definition.** The D2 spec must delete its Stream F lease, FR-023, US-14, its BDD scenario and its test 23, and reference §14's FR-019…FR-024 and FR-019a instead. §14 adopts D2's pre-built-`ToolResult` convenience so D2's new tools do not hand-roll the deferral shape. A structural test asserts exactly one lease primitive exists in `pkg/tools/browser`. |
| **A8** | **Fairness under sustained contention** is an explicit ADR open question. | **ASSUMED:** bounded wait then defer, no queue, no fairness guarantee — matching the ADR's stated scope. §14 fixes the bound, its config key and its clock seam so "unfair" is at least *bounded and testable*. A starvation-free queue is deferred, not forgotten. |
| **A9** | **Upgrade path for existing per-agent browser state.** | **DECIDED: discard, do not merge** — merging per-agent jars into a workspace jar would pool logins from agents that never shared them, a silent privilege grant at upgrade time. **And largely moot:** with `capture_shared_context` defaulting true (`defaults.go:671`), most installs have **no** per-agent CDP context to discard; what they have is one shared default-context profile. The release note must say operators re-log-in once, per workspace. |
| **A10** | **Operator of record for the 2026-08-31 rulings.** | **BLOCKING PREREQUISITE, promoted out of this table into SC-015.** The ADR names Daniel Piatkowski for D1.1a and D1.2; the remaining D1 rulings must carry the same attribution before implementation starts. Not something a spec can decide. |
| **A11** | **Two gateway processes on one `$OMNIPUS_HOME`.** The write lease is in-process only (FR-030, correctly — `fileutil.WithFlock` is a documented no-op on Windows). The pool adds a per-key on-disk launch lock, which inherits the same Windows no-op. | **DECIDED: out of scope, stated rather than silent.** Two gateways on one home are already unprotected for all six file stores on Windows (ADR-054 §5.1) and the pool neither worsens nor fixes that. On POSIX the per-key `flock` gives the same single-launch guarantee the current singleton lock gives. Filed as follow-up with ADR-054's `LockFileEx` work, not solved here. |
| **A12** | **Does a workspace browser outlive the last agent that can use it?** FR-026 said "roster change" without a predicate. | **DECIDED (FR-026a):** a workspace key is live while the workspace exists **and** has ≥1 browser-policy-allowed agent on its CoreTeam. Losing the last such agent closes the browser (the profile survives). This is also the prune's liveness predicate, so the two can never disagree. |
| **A13** | **What happens to a running sub-turn's browser work when the parent is cancelled?** | **DECIDED (FR-036):** ADR-057's inherited `routingSessionID` makes chat-wide Stop cascade to sub-turns, which cancels the in-flight tool call and releases the lease (FR-024). **No browser is closed** — the browser belongs to the workspace, not the turn, so a cancel must not log the operator out. |
| **A14** | **Is `ResolveBrowsingKey` evaluated once per turn or once per tool call?** | **DECIDED: once per tool call**, inside `ManagerResolver.ManagerFor` (FR-002a). Per-call is required anyway because the manager must be resolved per `Execute`; and since there is now exactly one key shape and no viewer-dependent branch, per-call resolution is **deterministic within a turn** — it cannot change under the caller the way the withdrawn attendance check could. No caching layer is specified; if profiling later demands one, it caches per turn, never across turns. |
| **A15** | **`max_browsers`' default value.** | **UNRESOLVED BY DESIGN — it is a measurement, not a decision.** FR-044/G-1 must produce the marginal per-Chrome RSS figure first. Recorded here so nobody ships a plausible-looking constant. The *shape* is decided: a positive integer, config key `tools.browser.max_browsers`, with the same reload-without-restart behaviour `max_total_tabs` already has (`coordinator.go::SetMaxTotalTabs`, `::ApplyRuntimeConfig`). |
| **A16** | **Does `chrome.tabCapture` actually work against a second Chrome's default context?** ADR D1.1a asserts each Chrome carries its own extension, so yes. | **UNRESOLVED — GATE G-2 (FR-045).** Flagged rather than assumed because this is the same *class* of claim that proved false for CDP contexts (`coordinator.go:330-348`, verified against real Chrome 150), and that falsification cost an entire design. Prove it with a spike **before** Stream P is built. |

**Corrections folded in above, listed so a reviewer can check them:** the ADR's `pkg/agent/loop.go:185` is `:279`; "six tool descriptions" is 5 model-visible strings + 2 Go comments + 1 unrelated SPA comment; `pkg/tools/base.go:241-251` is `:243-252`; `pkg/tools/resolvepath.go:695-709` is prose whose call is at `:713`; the viewer counters are cited by symbol (`ViewerAttached`/`ViewerDetached`) rather than by the two disagreeing line numbers the previous draft gave; `shared_control_test.go` has **eight** tests, not nine; the registered tool name is **`serve_web`** (`pkg/tools/web_serve.go:46`), not `web_serve`; `BrowserManagerForAgent` takes **one** argument today, not two.

---

## 13. Holdout evaluation scenarios (post-implementation; NOT in the TDD plan or traceability)

1. **(happy)** Operator opens the live panel in a workspace chat with Mia, logs into a real site, switches the chat to Jim, asks "what's open?" — Jim names the page and can act on it. The verbatim ADR §1.1 conversation, re-run.
2. **(happy)** Operator opens a *new* chat in the same workspace the next day and asks Ray to check the same site — still logged in, no re-auth.
3. **(edge)** Two workspaces for two clients, both logged into the same SaaS with different accounts. Each workspace's agents see only their own account, and nothing in either UI hints at the other.
4. **(error, REQUIRED UAT — US-8/AC3)** Operator asks Mia (policy-denied) what is open. She says she is not permitted to see the browser — she does not say the browser is empty, and she does not claim it is shared. **Transcript recorded and attached to the PR.** This is the only evidence for ADR criterion 3b that observes what the model actually *says*; §11 states plainly that no automated test covers it.
5. **(error)** A heartbeat for a custom agent on no workspace runs a browser step — it fails with `ErrNoBrowsingContext`'s named text, the log shows the refusal, and no Chrome was launched.
6. **(happy, the inverted case)** Operator asks Jim to delegate a research task to Ray and then closes the browser panel. Ray's sub-turn browses the workspace's signed-in session and completes — **it does not hit a login wall.** This is the D1.2 ruling's whole point, and it is the scenario the previous design deliberately broke.
7. **(edge)** The same delegation, but the task reaches an `ask`-policy tool. It is refused promptly with a reason, and the turn does not hang (#659).
8. **(edge)** Two agents on one workspace browse different sites simultaneously. Both complete; neither errors; the transcript shows at most one deferral apiece and no interleaved page state.
9. **(edge)** Operator saves an unrelated Setting mid-browse. Each workspace's Chrome pid is unchanged, the logins survive, and the panel keeps streaming.
10. **(edge)** Operator deletes a workspace whose browser had tabs open. That Chrome closes, other workspaces are unaffected.
11. **(edge)** An agent is added to a second workspace mid-session. Its next turn in the original chat still resolves to the original workspace (the chat's `workspace_id` wins) — no silent browser swap. Its next *heartbeat*, carrying no workspace id, is **refused as ambiguous** rather than silently picking one (FR-033).
12. **(edge, the pool)** Operator opens browsers in `max_browsers` workspaces, then starts work in one more. They get a clear refusal naming the cap — and, critically, **nothing they had open closed**.
13. **(error, the pool)** One workspace's Chrome is killed from outside (Activity Monitor / `kill`). That workspace's panel shows an error and recovers on next use with its login intact; every other workspace keeps streaming without interruption.
14. **(edge, memory)** Leave the gateway running with several workspaces browsed and then idle overnight. Chrome process count returns to zero, RSS returns to baseline, and the next morning's first browser call in any of those workspaces is still logged in.

---

## 14. Annex — the write lease (NORMATIVE; the D2 spec references this, and must not restate it)

**Ownership.** ADR-072 files the lease under D2.10, but ADR §4 calls it "the largest open risk **in D1**", and it is D1's re-key that creates the contention: before D1, two agents on one workspace had two browsers and could not collide. **Operator ruling, 2026-08-31: the lease belongs to this spec.** The sibling D2 spec had specced it independently, with an incompatible signature, over the same call sites — if both had landed, the seven action tools would have taken two unrelated mutexes and mutual exclusion would have been lost for whichever tool took only one. That is nondeterministic interleaving, which ADR §5 calls the most expensive failure class for an agent.

**Required action in the D2 spec** (tracked, not assumed): delete its Stream F lease scope, its FR-023, its US-14, its lease BDD scenario and its `TestLeaseWrite_SecondWriterDeferred`, and replace them with a reference to FR-019…FR-024 and FR-019a here.

### 14.1 API

```go
// pkg/tools/browser/lease.go (new)

// leaseWaitTimeout bounds how long an action tool waits for the browser's write
// lease before reporting contention. Config key: tools.browser.lease_wait,
// default 2s, reload-applied via ApplyRuntimeConfig like max_total_tabs.
//
// Relationship to the action-tool CDP timeout (MIN-007): it MUST be strictly
// less than the shortest action-tool timeout. If it were longer, a waiting tool
// could exhaust its own deadline inside the wait and return a CDP timeout
// instead of a deferral — an error where the contract requires a non-error.
// Asserted by TestWriteLease_BoundedWait.
var leaseWaitTimeout = 2 * time.Second

// leaseClock is the test seam for the bounded wait. Production is the real
// clock; tests substitute a fake. Named here because the previous draft
// required a "fake clock" without saying what it was attached to.
type leaseClock interface {
    Now() time.Time
    After(d time.Duration) <-chan time.Time
}

// acquireWrite is the single lease primitive in pkg/tools/browser. There is
// exactly one acquire symbol in the package; a structural test asserts it.
//
// It is a per-BROWSER (not per-manager-mutex) mutual exclusion held for the
// duration of ONE action-tool call. It is NOT m.mu and must never be taken
// while m.mu is held — an action tool blocks on CDP for seconds, and the
// ADR-038 "no lock across a blocking call" discipline forbids it.
//
// Returns:
//   ok=true                 -> caller holds the lease; MUST defer release()
//   ok=false, holder="jim"  -> caller must defer via deferredResult(...)
//
// It waits up to leaseWaitTimeout (cancellable by ctx, so a cancelled turn
// parks no goroutine) before returning ok=false, so a lease held by a fast
// action is not reported as contention.
func (m *BrowserManager) acquireWrite(
    ctx context.Context, key BrowsingKey, holderAgentID string,
) (release func(), ok bool, holder string)

// leaseWrite is the convenience wrapper every action tool actually calls. It
// adopts the D2 spec's pre-built-*ToolResult* shape so D2's five new action
// tools do not hand-roll the deferral body, while keeping D1's stronger
// primitive underneath (cancellable, bounded, names the holder).
//
//   deferred, release := leaseWrite(ctx, mgr, key, agentID, "browser_click")
//   if deferred != nil { return deferred, nil }
//   defer release()
//
// deferred is nil iff the lease was acquired. When non-nil it is a NON-error
// result whose body is {"deferred": true, "reason": <text naming the holder>}.
func leaseWrite(
    ctx context.Context, m *BrowserManager, key BrowsingKey, agentID, toolName string,
) (deferred *tools.ToolResult, release func())
```

### 14.2 Rules

1. **Composition order is fixed:** `controlledResult` first (a human holding the wheel outranks an agent queue — ADR-038 D6), then `leaseWrite`. Both produce the same `{"deferred": true, "reason": …}` non-error shape with different reason text. When a human holds control, the lease is **never acquired** (FR-022).
2. **`controlledResult` must ask about the resolved key** (FR-002c). It currently asks `IsControlled(defaultSessionID)` (`tools.go:963`); left unchanged, it returns `false` forever once the live registry is re-keyed, and the human control lock silently stops working — a regression of a shipped safety property.
3. **Membership is a rule, not a list** (FR-019a): every tool in `pkg/tools/browser` that mutates page or tab state takes the lease. The exemption is a **closed, named set of five**:

   | Exempt tool | Why |
   |---|---|
   | `browser_screenshot`, `browser_get_text`, `browser_wait` | Read-only; ungated today |
   | `browser_snapshot` (D2 FR-018) | Read-only |
   | `browser_handle_dialog` (D2 FR-035) | **Recovery.** A modal blocks every CDP command on its tab, so the blocked `browser_click` still holds the lease. Leasing the recovery tool makes it defer behind the wedge it exists to clear — the tab stays stuck for agent and human alike |

   A tool that is neither leased nor exempt fails `TestWriteLease_EveryActionToolIsLeased`, which enumerates the **registry**, not a hand-written list. This is what makes D2's four remaining new action tools automatically in scope.

   **Amended 2026-08-31** after the D2 spec found the conflict: this set was three, and `TestWriteLease_EveryActionToolIsLeased` would have gone red the moment `browser_snapshot` or `browser_handle_dialog` registered — blocking D2's Streams C and D from ever landing green.
4. **`release()` is idempotent and MUST run via `defer`** in every leased tool, so a panic, a CDP timeout or a cancelled context cannot wedge the browser (FR-024).
5. **Lock order** is `writeLease → pool.mu → m.mu`, never reversed; `m.mu` is never held across `acquireWrite` or any CDP call.
6. **In-process only** (FR-030). The lease deliberately does not use `fileutil.WithFlock`, which is a documented no-op on Windows (`pkg/fileutil/flock_windows.go`) and would give a false cross-process guarantee. Two gateways on one home are out of scope — §12 A11.
7. **No fairness guarantee** under sustained contention (§12 A8). The bound is what is promised, and FR-023 tests it.

---

## 15. Round-2 review disposition (all 29 findings)

| ID | Disposition | Where / evidence |
|---|---|---|
| **CRIT-001** — isolation is off by default; enabling it breaks the live panel | **Superseded by ruling, then fixed.** The operator rejected the trade; ADR D1.1a replaces CDP-context isolation with **Chrome-process + profile isolation**, which delivers both. The finding's diagnosis was correct and is preserved verbatim in §0.1 with its evidence | §0.1, FR-031, FR-037, FR-045; gate G-2 |
| **CRIT-002** — tools hold a manager bound at registration | **Fixed.** `register.go:41-84` and the 11 `mgr:`-bearing structs added to §2.1; `ManagerResolver` added to the interface contract; FR-002a; a **structural** test (no `*BrowserManager` field) and an **end-to-end** test through `registerSharedTools` that is red today | §2.1, §3.1, FR-002a, tests 4 + 38, SC-001 |
| **CRIT-003** — reload prune keys off agent ids | **Fixed.** `loop.go:2849-2871` verified; FR-026a makes the predicate the live-key set with an explicit liveness definition (§12 A12); FR-026b makes registration idempotent per key; the reload BDD now specifies **two agents** and asserts `pool.Close` count **zero** | FR-026a, FR-026b, tests 32/33/42, US-17 |
| **CRIT-004** — the lease is double-specified with incompatible APIs | **Fixed.** §14 is the single normative definition, per operator ruling. It keeps D1's stronger primitive and adopts D2's pre-built `ToolResult` convenience, so neither team rewrites call sites. A structural test asserts one acquire symbol. The required D2-spec deletions are named | §14, §12 A7, FR-019…FR-024 |
| **CRIT-005** — `controlledResult` and ~15 gateway sites still use the constant; the nominated guard cannot catch it | **Fixed, and the enumeration was larger than reported.** §2.2 counts **37** executable references (13 in `browser_ws.go`, not ~15, plus 24 elsewhere the review did not reach) plus 12 comments. FR-002b deletes the constant; FR-002c re-keys `controlledResult` and is assigned to **Stream A**, not Stream D. Regression list corrected: `shared_control_test.go` is **8** tests and is **not** the guard; `tools_control_test.go` (3 tests, `:59/:106/:153`) is | §2.2, FR-002b, FR-002c, §10.1, SC-009, SC-013 |
| **CRIT-006** — unattended contexts have no disposal path | **Re-scoped by the D1.2 ruling, and the underlying leak is closed.** Sub-turns no longer create anything to leak (FR-009, FR-026c). The finding's *general* point — that `ReapIdleSessions` deletes `m.sessions` entries and never disposes a browser (verified: its only removal is `delete(m.sessions, sessionID)`) — stands and is fixed by FR-040's whole-Chrome idle close, which did not exist in any form before | FR-009, FR-026c, FR-040, tests 31 + 41 |
| **MAJ-001** — the ladder cannot be evaluated in the order it specifies | **Fixed by deletion.** The step that could not run first (the attendance check) is withdrawn. The ladder is now three unambiguous rungs with no forward reference | §3.1 `resolve.go`, FR-007 |
| **MAJ-002** — "attended" is a proxy that does not implement the ruling | **Moot.** The ruling it failed to implement was itself reversed; there is no attendance concept | §0.2, §12 A2/A3 |
| **MAJ-003** — a fresh install has no workspaces | **REJECTED on the central claim; residual accepted.** *"Nothing seeds a workspace on a fresh install"* is false: `ensureDefaultWorkspace` (`pkg/gateway/rest_workspaces.go:468`) runs on **every** boot (`pkg/gateway/gateway.go:5013`) and creates "My Workspace" with `defaultWorkspaceTeam(cfg)` = `coreagent.All() ∩ configured agents` (`pkg/gateway/rest_workspace_delegation.go:359-379`) — which includes **Jim and Ray**, the two browser-policy-allowed agents. A fresh install resolves. **Accepted residual:** a *custom* agent is deliberately never auto-added to a team (`gateway.go:5018-5025`), so it resolves to nothing — and the panel's message for that case is misleading. FR-008a and US-14 fix the message; no workspace seeding is added | §6, US-14, FR-008a |
| **MAJ-004** — the "no wire change" claim passes only because SC-007 measures shape | **Fixed.** The claim is restated honestly; SC-007 gains a third, **behavioural** condition (test 46) that fails if `session_id` is not binding; the two replacement descriptions must match FR-016's verbatim text; `BrowserInspectRequest` is decided explicitly (its `session_id` is a *browser* session id, so it gains no chat semantics and resolves from the agent under FR-033). The `browser_started`→`state` persisted-JSON question is confirmed safe — `grep -rn "browser_started" src/` returns nothing | SC-007, US-10/AC3+AC4, FR-016, §6 SPA |
| **MAJ-005** — ADR criterion 3b has no automated coverage | **Fixed by (a) + (b).** FR-014a strengthens the artefact so the denial for a `browser_*` tool **names the browser surface** rather than the system-wide generic `"Tool execution denied by policy."` (`tool_denial.go:206-210`), and test 10 asserts that string. Holdout 4 is promoted to a **required UAT with a recorded transcript** (US-8/AC3), and §11 says plainly that no automated test observes what the model says | FR-014a, US-8, test 10, §13 holdout 4 |
| **MAJ-006** — the boot warm path is broken and unmentioned | **Fixed.** `pickWarmBrowserManager` (`gateway.go:3373`, called `:3562`) added to §2.1 as **modifies**; FR-016b requires it to warm the resolved key and to skip with a single INFO (not WARN) when nothing resolves; test 24 | §2.1, FR-016b, test 24 |
| **MAJ-007** — the capture registry has scope but no requirement | **Fixed.** FR-016a re-keys `captureRegistry` (`browser_webrtc.go:70-78`) to the browsing key and states the consequence: **one capture session per workspace browser**, and ADR-048's "requesting agent" conflict rule collapses because agents no longer have disjoint tab sets | FR-016a, test 25, §6 |
| **MAJ-008** — the leased set is a closed enumeration that D2 will grow | **Fixed.** FR-019a replaces the enumeration with a rule plus a **registry-driven** structural test; the exemption is a closed set of three. D2's five new action tools are in scope automatically | FR-019a, §14 rule 3, test 18 |
| **MAJ-009** — `browserCtxIDs` as a map is dead structure | **Accepted, and then superseded.** The review's preferred fix (keep the single field, add `m.key`) was right; the D1.2 ruling then removed the second key shape entirely and FR-031 retires CDP contexts altogether, so `browserCtxID` is neither a map nor used. The map is not built | §0.2, FR-031 |
| **MAJ-010** — D1 creates the scenario that makes #659 dangerous | **Fixed, and more urgent under the new ruling.** With delegated sub-turns now sharing a *signed-in* browser, an `ask`-policy hang is worse, not better. FR-032 makes auto-deny a **D1** requirement rather than a D2 assumption; §6 records #659 as a prerequisite; US-5/AC3 and test 30 cover it | §6, FR-032, US-5/AC3, test 30 |
| **MAJ-011** — sorted-first tie-break selects a cookie jar | **Fixed, with the stronger option.** FR-033 **refuses** rather than arbitrating: for a filesystem mount the worst case is the wrong directory; for a browser it is acting as the wrong signed-in identity. The WARN is required on **both** resolution paths (`FindForAgentPreferring`'s fast path suppresses it today, `find_for_agent.go:168-176`). Cost recorded: a multi-workspace agent's heartbeat loses the browser rather than guessing | FR-033, US-6/AC3, US-11/AC2, test 3 |
| **MIN-001** — `shared_control_test.go` is 8 tests, not nine | **Fixed.** Verified: `:35, :55, :74, :92, :109, :138, :157, :186` | §10.1 |
| **MIN-002** — `tools_control_test.go` is absent from the regression list | **Fixed.** Added, with its three test names and line numbers, and made SC-009's guard | §10.1, SC-009 |
| **MIN-003** — the spec says `web_serve` twice | **Fixed.** Both occurrences now `serve_web`, with the evidence (`pkg/tools/web_serve.go:46`, `const ToolNameWebServe = "serve_web"`) | §1 out-of-scope, §5 |
| **MIN-004** — viewer-counter line numbers disagree with each other | **Fixed.** Cited by symbol (`ViewerAttached`/`ViewerDetached`) per the root `CLAUDE.md` rule, with a stated citation policy at the top of the file | §2.1, citation policy |
| **MIN-005** — FR-015 requires "corrected" strings without specifying them | **Fixed.** FR-034 specifies the replacement literals, and test 9 asserts the new text is present rather than only that the old text is gone. With one key shape, "shared across this workspace" is now accurate for every case | FR-034, test 9 |
| **MIN-006** — A10 unresolved with no gate | **Fixed.** Promoted from a table footnote to **SC-015, a blocking prerequisite** | SC-015, §12 A10 |
| **MIN-007** — `leaseWaitTimeout` has no config key, no timeout relationship, no named clock seam | **Fixed.** §14.1 names the config key (`tools.browser.lease_wait`), states the required relationship to the action-tool CDP timeout (strictly less, with the reason), and names `leaseClock` as the seam | §14.1 |
| **MIN-008** — `loop.go:270-279`'s standing comment is not addressed | **Fixed.** FR-002d requires the comment be **replaced**, not deleted: the map stays a map (the gateway still needs a specific manager), and the comment must say the key is now the browsing key | FR-002d |
| **MIN-009** — SC-003 asserts key construction, not context creation | **Fixed.** FR-035 adds the behavioural assertion — every `pool.Acquire` key traced to a `ResolveBrowsingKey` return in the same turn — and SC-003 is rewritten around it | FR-035, SC-003, test 34 |
| **OBS-001** — the three-state enum is largely a rename | **Partially accepted; enum retained with reasoning.** The observation is fair: the code already distinguishes two states via `browser_started` (`tabs.go:58,66`). **Retained anyway** because the alternative (`browser_started` + a second `tabs_empty` boolean) is two booleans with four representable combinations of which three are legal — a shape that cannot be closed, so SC-006's "no fourth value for any input" would be untestable. A closed enum makes the falsifiability claim real. Recorded rather than silently kept | US-7/AC4, SC-006 |
| **OBS-002** — three new concepts where one would do | **Accepted.** Two of the four are gone: `BrowsingKeyKind` is not built (one key shape), and the `browserCtxIDs` map is not built. `ViewerCounter` is also gone; `BrowserManager.Viewers()` survives as a plain accessor the reaper and idle-close need. Net new types: `BrowsingKey`, `TabState`, `BrowserResolveOutcome`, `ManagerResolver`, plus the pool — the last of which is new *scope*, not surplus abstraction | §0.2, §3.1 |
| **OBS-003** — the lease has three homes and should get a fourth | **Partially accepted.** The diagnosis (three homes) is right and the ruling settled it, but a separate spec file would make it a fourth. Instead it is a **§-numbered annex inside this spec** (§14), which the D2 spec references — the review's own suggested shape, kept in one file so D1 and D2 still ship independently | §14, §12 A7 |

---

**Next:** resolve SC-015 (attribution) in ADR-072; run gates G-1 and G-2; land Stream A and the §0.4 set; then build Stream P. The D2 spec must be edited to delete its lease and reference §14 before either spec is implemented.
