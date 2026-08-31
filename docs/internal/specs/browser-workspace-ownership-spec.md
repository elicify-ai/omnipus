# Spec — Browser ownership: workspace-scoped browsers (ADR-072 **D1**)

- **Source ADR:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions.md` — **D1 only (D1.0, D1.0a, D1.1, D1.1a, D1.2, D1.3–D1.5)**, plus the write lease the ADR files under D2.10 but §4 attributes to D1 (see §14).
- **Round-1 ADR review folded in:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions-review.md`.
- **Round-1 spec review folded in:** `docs/internal/specs/browser-workspace-ownership-spec-review.md` — verdict **BLOCK**, 29 findings. Every one is dispositioned in **§15**; one is rejected with evidence and says so there.
- **Round-2 spec review folded in:** `docs/internal/specs/browser-workspace-ownership-spec-review-round2.md` — verdict **BLOCK**, 29 findings (4 CRITICAL / 14 MAJOR / 8 MINOR / 3 OBSERVATION). Every one is dispositioned in **§16**; three are rejected or narrowed with evidence and say so there. *(Both review files use the same finding-id prefixes; round-2 numbers them from 101 to keep them distinguishable. §15 and §16 are separate tables and neither supersedes the other.)*
- **Amends:** **ADR-043 D1** (one shared Chrome for the process — *this spec replaces it with a pool*), **ADR-043 D2** (per-agent CDP browser context — *replaced by per-workspace Chrome profiles*) and **ADR-043 D3** (live-view binding). Read ADR-043 first; D1 has the largest blast radius of anything in ADR-072.
- **Sibling spec:** D2 (capability). **This spec owns the write lease — §14 is its single normative definition.** The D2 spec must delete its own lease FR/US/stream/test and reference §14 (operator ruling, 2026-08-31).
- **Worktree:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf` · **Branch:** `feat/browser-streaming-performance`
- **Status:** Draft for implementation, **gated on two measurements** (§0.3) — not on a ruling. All design questions are decided.
- **Operator rulings folded in (2026-08-31, Daniel Piatkowski):** workspace is the isolation axis, not the agent and not the conversation; **isolation is by Chrome process + profile directory, not by CDP browser context** (D1.1a); **every agent on a workspace shares its browser and its logins, including unattended delegated work** (D1.2, superseding the earlier same-day ruling); every turn runs in a workspace (no workspace-less fallback); the browser seed stays Jim + Ray only; the write lease belongs to this spec.

**Citation policy.** `pkg/agent/loop.go`, `turn.go` and `subturn.go` are ~11k-line files under constant churn; per the root `CLAUDE.md` this spec cites them as `file::symbol`. Line numbers appear only where the file is stable or where the exact line *is* the evidence (a config seed, a literal string, a hardcoded constant). Every `file:line` below was re-verified on this worktree on 2026-08-31.

> **Why the word "verified" is no longer used as a shortcut.** The previous draft marked two statements about `ReapIdleSessions` *verified* and both were false (round-2 CRIT-102; the ADR now carries it in its own §8 corrections log). The claim had never been run against the code — it was a plausible reading that survived because the label discouraged re-checking. **Every code claim in this draft was re-derived from source on 2026-08-31, including the ones the previous draft had already labelled verified.** Where a claim is narrower than it sounds, the narrowing is written into the claim rather than left to the reader.

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

**One prior blocker, checked and dismissed — do not re-raise it.** ADR-043 rejected multiple Chromes partly because the DevTools port was pinned at 9223 and a dynamic per-manager port could not be followed by the compiled Landlock/seccomp allow-list. That reason has evaporated: Chrome is driven over `--remote-debugging-pipe` on inherited fds 3/4 (`pkg/tools/browser/exec_resolver.go:60` — *"There is NO `--remote-debugging-port` — CDP flows over the inherited fd 3/4"*) and the allow-list entry was removed with the port (`pkg/gateway/sandbox_apply.go:412-417` — the removal NOTE; `:405-409` is the `enforcePortRules` computation above it, which the previous draft's `:405-417` span wrongly swept in. Round-2 MIN-104). **N Chromes are N pipes and nothing to allow-list.** (`pkg/tools/browser/manager.go:1330-1345` still describes a pinned port; that comment governs the legacy no-coordinator fallback, not the coordinator path this spec changes. It should be corrected in passing.)

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

#### 0.3.1 How each gate is enforced — one is mechanical, one is a named human gate

The previous draft declared both gates "blocking" and gave neither a failing check. **G-2's was worse than absent: it was a test that reports green without running.** Test 37 was specified with `skipIfNoBrowser`, which has **two** skip paths (`pkg/tools/browser/browser_e2e_test.go:57-112`), each of which ends the test as a PASS:

1. `if os.Getenv("CI") != "" && os.Getenv("OMNIPUS_BROWSER_E2E") == "" { t.Skip(...) }` (`:66-68`) — in CI, without the opt-in env var, it always skips;
2. no probeable Chrome/Chromium on `$PATH`, in the macOS `.app` locations, or in the managed install root — the probe ladder at `:69-110` falls through to `resolveTestBinary(t)` at `:111`, which the function's own comment says *"calls `t.Skipf` … when even the managed/download path comes up empty"*. It skips rather than fails.

G-2 guards the single load-bearing assumption of D1.1a, and the equivalent claim for CDP contexts proved **false** against real Chrome 150 (`coordinator.go:330-348`). A skip standing in for that proof is exactly the shape `docs/internal/false-green-patterns.md` exists to catch.

**G-2 is therefore respecified as a mechanical gate (FR-045, SC-012a).** Three requirements, all of which the implementing PR must satisfy before Stream P's first commit:

- Test 37 is **not** `skipIfNoBrowser`. It uses a new helper, `requireBrowserOrFail(t)`, which resolves a browser through the **same** three-source ladder `skipIfNoBrowser` uses and calls `t.Fatal` — never `t.Skip` — when it finds none. A missing browser on the G-2 runner is a gate failure, not a pass.
- The gate job sets `OMNIPUS_BROWSER_E2E=1` explicitly, and runs `-run '^TestSpike_CaptureAgainstSecondChrome$'` with `-count=1`.
- The run's receipt is captured **without a pipe** (`cmd > log 2>&1; echo "exit=$?"`) and the log is asserted to contain neither `--- SKIP` nor `no tests to run`. A gate whose own log says SKIP has not run, and a `-run` pattern that matches nothing exits 0.

The natural home is the `e2e` gate on the `ci-omnipus` Fly worker (`deploy/ci-worker/runci.sh`), which already provisions a real Chrome; **read `deploy/ci-worker/CLAUDE.md` before trusting any verdict it returns** — its two documented false-signal traps (stale-checkout false-RED, wrapper-exit-code false-GREEN) both apply here.

**G-1 is a human gate, and this spec says so rather than pretending otherwise.** There is no honest mechanical form: the number is an RSS measurement on one host, and a unit test asserting "a measurement file is non-empty and dated" would pass on a fabricated file — a check that cannot distinguish a measurement from a plausible number is not a gate, it is a second place to write the guess. **G-1 is a review gate. Its owner is the implementing PR's human reviewer, and its artefact is named in SC-012:** the raw `ps`/`smem` output for N=1…4 Chromes, the host's total RAM, and the arithmetic from that to `max_browsers`' default, pasted into the PR body. A reviewer who cannot see the arithmetic must not approve. The one mechanical half that *is* real is negative: `TestConfig_MaxBrowsersDefaultIsNotZeroOrRound` (test 51) fails if the shipped default is 0, 5, 10 or 100 — the shapes a guess actually takes — which does not prove a measurement happened but does make the most common failure visible.

**Neither gate is satisfied by a green CI run alone.** SC-012 and SC-012a state each one's failure condition separately.

### 0.4 What is not gated

Everything that is about *ownership* rather than *partitioning* — and this is the part that fixes the reported defect:

- one manager and one tab set per workspace (FR-001, FR-002, FR-002a, FR-002b, FR-002c) — **this alone fixes ADR criteria 2 and 3**, because handover is broken by the *manager* split, not by any partition;
- the resolution ladder, its named failure, and a distinguishable panel failure reason (FR-007, FR-008, FR-008a);
- the reload prune and per-key idempotent registration (FR-026a, FR-026b);
- the three-state tab answer, the **deletion** of the false shared-browser claim, its **interim** replacement literal, and the denial that names the browser (FR-013, FR-014, FR-014a, FR-015, FR-034);
- the write lease (§14);
- audit, gateway resolution, capture registry, warm path (FR-016, FR-016a, FR-016b, FR-017, FR-018, FR-027);
- the operator-facing browser close action and the team-membership disclosure (FR-046, FR-047) — neither depends on the pool's existence, and FR-046 is the remedy the pool-full refusal names once the pool lands.

**Sequence: land §0.4 first, run G-1 and G-2 in parallel, then build the pool.**

**One thing is deliberately NOT in §0.4, and the previous draft had it there.** The **final** description literal — the one that tells the model each workspace has its own browser and its own logins — asserts isolation, and isolation does not exist until Stream P ships FR-037. Shipping it early would make the product state a false ownership claim to the model and to the operator, which is the precise defect ADR-072 §1.1 exists to fix; the previous draft's justification ("the intermediate state is exactly today's behaviour, so it is not a regression") is true of the cookie partitioning and false of the sentence describing it. **FR-034 therefore has two literals with two landing points** (§3.3): an interim one that claims only what Stream A makes true, and a final one that lands in the same commit as FR-037. §5 records the general form as a non-behaviour.

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
- **The pool's lifecycle edges**, which the previous draft left to inference and which are the bulk of round-2's MAJOR findings: cap edge semantics and the cap's relationship to the existing global tab budget (FR-038a); the whole-Chrome idle window as a named config key with a named caller and a specified post-close state (FR-040a); boot reconciliation of the N ownership markers so orphan Chromes cannot sit outside the cap (FR-042a); per-key stale-singleton cleanup, without which FR-043's "the profile survives" is false after a crash (FR-042b); the profile directory's **deletion** path (FR-043a); the profile root's relationship to `cfg.ProfileDir` and to the managed-Chromium install root (FR-037a); and the boot preprovision path, which a lazily-created pool silently breaks (FR-016c).
- **An operator-facing "close this workspace's browser" action** (FR-046) — REST + UI. Without it, the pool's refusal at the cap has no remedy an operator can perform, and the previous `ErrBrowserPoolFull` text named two actions that do not free a slot.
- **The team-membership elevation-of-privilege disclosure** decided in ADR D2.11 (FR-047): the Workspace → Team editing UI must state, at the point of adding an agent, that the agent gains every live browser session on that workspace. **Claimed here rather than left ownerless.** §1's out-of-scope list excludes only D2.11's *information-disclosure* bullet, so the *elevation-of-privilege* bullet was in scope by wording and owned by neither spec; D1.2 makes it strictly worse than when the ADR wrote it, because unattended delegated work now inherits those logins too.
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

- Everything in **D2** — role/accessible-name selection, actionability, `browser_select_option` / `browser_press_key` / `browser_hover` / `browser_upload_file` / dialog handling / `browser_snapshot`, tier assignment (D2.8), policy seeding (D2.9), and **only** the D2.11 *information-disclosure* bullet (the snapshot's form-value exposure; D2.11's *elevation-of-privilege* bullet is claimed here as FR-047, and its *repudiation* bullet is already FR-027). **But:** D2's new action tools inherit §14's lease *by rule* (FR-019a), and D2.9's `ask` seed has a prerequisite that D1 makes dangerous (FR-032). **One requirement runs the other way:** §14 rule 3's exemption rule only holds if D2 registers `browser_handle_dialog` ungated by `controlledResult` as well as unleased — see §12 A17, which is the single cross-spec obligation D1 places on D2 beyond deleting its duplicate lease.
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
| `AgentLoop.BrowserManagers()` | **modifies** | Snapshot slice; membership changes with the re-key. **Three consumers, not one:** the 1-minute idle sweep (`gateway.go:5342`), gateway `Close()`, and — the one the previous draft missed — boot `Preprovision` (`gateway.go:2286`). See the `Preprovision` row below and FR-016c |
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
| `BrowserManager.ReapIdleSessions` (`manager.go:2986`) | **extends** | Per-tab TTL, `se.viewers > 0` pin, zero-tab `emptySince` branch — all implemented. **CORRECTED (round-2 CRIT-102):** the previous draft claimed here and in §15 that this method "deletes `m.sessions` entries only; it never touches the coordinator and never closes a browser", marked *verified*. **That was false.** It collects `se.browserCancel` into `reapedBrowsers` in **both** removal branches (`:3027-3032` stranded-empty-session, `:3073-3078` all-tabs-idle), executes those cancels after unlocking (`:3123-3125`) — cancelling the **browser-owning** chromedp context — cancels per-tab contexts at `:3106-3107`, and reaches the coordinator through `m.releaseGlobalTab()` (`:3118` → `:3358-3365` → `coord.ReleaseTab(agentID)`). **The narrow claim that survives, and it is the only one this spec relies on:** `ReapIdleSessions` never calls `RemoveAgent` and never calls `disposeBrowserContextRaw`, so the coordinator's own per-key state and the Chrome **process** are untouched. Whole-Chrome close is genuinely new work (FR-040), but the disposal machinery is not absent, and the reaper↔pool contract is therefore a real interaction that FR-040a must specify — not a greenfield hook |
| the idle sweep goroutine (`gateway.go:5321-5352`, `const reapInterval = time.Minute`) | **extends** | The **named caller** the previous draft never identified. It ranges `agentLoop.BrowserManagers()` and calls `mgr.ReapIdleSessions()` on a 1-minute ticker, each tick individually `recover()`ed. FR-040a hangs the whole-Chrome idle close off this same tick, after the per-manager loop. The interval doc (`:5310-5320`) states the invariant a second TTL must also respect: the sweep interval must stay well under the TTL or the TTL becomes a floor |
| `browser.BrowserBuiltinMetadata` (`metadata.go:36-51`) | **modifies** | **Absent from the previous draft entirely (round-2 MAJ-105).** Constructs **all eleven tool structs a second time**, with a nil manager, for the central `BuiltinRegistry`. FR-002a's struct-shape change breaks this file too, and it is the second construction site nobody would find from `register.go` |
| `CaptureSession` (`capture_session.go:258`), `LiveViewRegistry` (`live.go:322`), `LiveView` (`live.go:1324`) | **reuses (must NOT be "fixed")** | Three **non-tool** exported structs that legitimately hold a `*BrowserManager` field. They are not tools and must keep it; test 4's predicate is scoped to types implementing `tools.Tool` for exactly this reason (FR-002a). `BrowserCoordinator.managers` (`coordinator.go:186`) is a fourth. `LiveViewRegistry`'s doc comment (`live.go:316-320`) additionally asserts *"a BrowserManager is itself scoped to one agent (pkg/agent/loop.go's per-agent manager map, ADR-038 D4)"* — false after the re-key, and corrected with it (FR-002d) |
| `BrowserManager.Preprovision` (`manager.go:3218-3223`) / `AgentLoop.BrowserManagers()` (`loop.go::BrowserManagers`) | **modifies** | **The boot role the previous draft's `BrowserManagers()` row omitted (round-2 MAJ-104).** `gateway.go:2286` ranges `BrowserManagers()` in a goroutine per manager to call `Preprovision`, which starts the managed-Chromium download **at boot** instead of at the first browser tool call — the explicit purpose recorded in `BrowserManagers()`' own doc comment. Under a lazily-created per-workspace pool that snapshot is **empty at boot**, so `Preprovision` never fires and a fresh install's first browser call blocks on a multi-hundred-megabyte download. Fixable cleanly: `Preprovision`'s entire body is `if m.cfg.CDPURL != "" { return "", nil }; return m.resolveExecPath(ctx)` — it resolves a binary path from config and needs **no** key, no manager and no live Chrome (FR-016c) |
| `cleanStaleSingletons` (`coordinator.go:1488-1498`, called `:1235`) | **modifies** | **Zero mentions in the previous draft (round-2 MAJ-103).** Removes Chromium's stale `SingletonLock`/`SingletonCookie`/`SingletonSocket` after an ungraceful exit, and is called with `c.cfg.ProfileDir` **only**. Each per-key profile gets its own singleton files; nothing would clean them, so after a crash that workspace's next Chrome launch fails and **FR-043's "the profile survives, so the login survives" is false in the exact case it exists for** (FR-042b) |
| `BrowserCoordinator.ApplyRuntimeConfig`'s ProfileDir branch (`coordinator.go:681-687`) | **reuses** | Today a `profile_dir` change on reload logs a WARN and is **not applied** to the running Chrome (*"applies after gateway restart"*). FR-037a preserves that behaviour per key rather than inventing a relocation (§12 A18) |
| `resolveScheduleWorkspaceID` (`pkg/gateway/schedules.go:581-639`) → `stampScheduledSessionWorkspace` (called from `pickSession`, `:513`+`:527-576`, itself called on every fire at `:141`) | **reuses** | **The shipped write path the previous draft did not cite, and whose absence made FR-033's premise wrong (round-2 MAJ-113).** A fired schedule's workspace is resolved and stamped onto the session meta **before** `ProcessScheduled` runs, and `ProcessScheduled` reads it back into `processOptions.WorkspaceID` (`loop.go:6934-6946`, assigned `:6957`). **Heartbeats are workspace-scoped by construction:** the reconciler names each job `heartbeat:<workspaceID>:<agentID>` (`heartbeat_schedule.go:30-33`) and `workspaceIDFromHeartbeatJobName` (`schedules.go:654`) parses it back out — so a heartbeat turn reaches `ResolveBrowsingKey` **rung 1**, never rung 2's ambiguity refusal. See §6 and §16 MAJ-113 for what this leaves as the real residual case |
| `BrowserConfig.PageTimeout` (`manager.go:35`, default 30s `:123`, config key `tools.browser.page_timeout` → `PageTimeoutSec`, `config.go:3632`, applied `loop.go:2311-2312`) | **reuses** | The "shortest action-tool timeout" §14.1 required `leaseWaitTimeout` to be strictly less than, and never named. Both values are operator-settable and nothing validates the relationship (FR-023a) |
| `maxTotalTabs`' unlimited semantics (`coordinator.go:785-788`) | **reuses (as the precedent)** | `if c.maxTotalTabs <= 0 { reserve; return true }` — 0 and negative mean **unlimited**, guarded by `TestCoordinator_UnlimitedDefault_AllowsPastOldCap`. `max_browsers` adopts the same *shape* and a different *default*, and FR-038a says why |
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

**The arithmetic, corrected (round-2 MIN-101).** The quoted command returns **57** lines, not 37, and the previous draft's "12 comment references" was wrong:

> **57 grep hits = 37 usages + 2 declarations + 18 comments.**

- **2 declarations:** `tools.go:63` (`DefaultSessionID`) and `tools.go:69` (the package-private alias `defaultSessionID`).
- **18 comments:** `tools.go:50, :62, :65, :68`; `tabs.go` — none; `inspect.go:116`; `capture_session.go:708`; `live.go:376`; `browser_ws.go:154, :1207, :1214, :1437, :1526, :1579, :1619, :1722, :1832`; `gateway.go:3602`; and **`pkg/config/config.go:3892`**, a file the previous draft's inventory omitted entirely (it is comment-only — one hit, no executable reference — which is why the per-file table above still has no row for it).

Per-file the raw hits are: `tools.go` 15 (9 exec + 2 decl + 4 comment), `browser_ws.go` 22 (13 exec + 9 comment), `tabs.go` 6, `inspect.go` 4 (3 + 1), `capture_session.go` 3 (2 + 1), `live.go` 2 (1 + 1), `browser_webrtc.go` 2, `gateway.go` 2 (1 + 1), `config.go` 1 (comment). The 37 executable line numbers in the table above were **each re-checked individually and all 37 are correct**.

**Stream E's previous scope named three call sites.** It was short by an order of magnitude, and the three it named were the `BrowserManagerForAgent` calls, not these.

### 2.2a The test surface — 364 references, and the previous draft budgeted none of them

Round-2 CRIT-101/MIN-102. `grep -rc "DefaultSessionID\|defaultSessionID" pkg --include "*_test.go"` returns **364 references across 25 test files** — roughly **ten times** the non-test surface SC-013 measures, and none of it appeared anywhere in the previous draft's estimate, streams or regression list.

| File | Refs | | File | Refs |
|---|---|---|---|---|
| `pkg/tools/browser/tabs_test.go` | 100 | | `pkg/tools/browser/live_tabchange_coordination_test.go` | 7 |
| `pkg/tools/browser/tab_adoption_e2e_test.go` | 41 | | `pkg/tools/browser/live_deadlock_test.go` | 7 |
| `pkg/tools/browser/idle_reaper_test.go` | 33 | | `pkg/tools/browser/tools_control_test.go` | 6 |
| `pkg/tools/browser/switch_tab_same_index_recapture_test.go` | 26 | | `pkg/tools/browser/inspect_test.go` | 5 |
| `pkg/tools/browser/reaper_edge_test.go` | 26 | | `pkg/tools/browser/coordinator_review_test.go` | 4 |
| `pkg/tools/browser/focus_emulation_test.go` | 24 | | `pkg/tools/browser/live_test.go` | 3 |
| `pkg/tools/browser/switch_tab_activation_test.go` | 20 | | `pkg/gateway/browser_ws_fixwaveb_test.go` | 3 |
| `pkg/tools/browser/switch_tab_capture_chain_test.go` | 18 | | 5 files × 2 refs | 10 |
| `pkg/tools/browser/reaper_lifecycle_test.go` | 15 | | 5 files × 1 ref | 5 |
| `pkg/gateway/browser_ws_test.go` | 15 | | **Total** | **364** |

**This is the finding that made §10.1 unsatisfiable, and it is a compile error, not a style point.** FR-002b **deletes** both symbols. §10.1 then required four of these files — `tab_adoption_e2e_test.go` (41), `idle_reaper_test.go` (33), `reaper_edge_test.go` (26), `reaper_lifecycle_test.go` (15), **115 references between them** — to keep passing **unmodified**. A file referencing a deleted identifier does not fail an assertion; it fails to build, and takes the whole package's test binary with it. The two requirements could not both hold.

**Resolution (FR-002e, and §10.1's wording is corrected to match).** The migration is **mechanical and in scope**, and it is not permitted to be done with an alias:

1. Every test-side `DefaultSessionID`/`defaultSessionID` argument is re-pointed at a browsing key the test constructs through the same seam production uses. Tests get a `newTestBrowsingKey(t, workspaceID)` helper in the package's existing test support file — **not** an exported literal constructor, which §5 forbids.
2. **No `defaultSessionID` alias survives in test code either.** SC-013 counts non-test references, so a test-only alias would leave the constant alive, the reaper suite asserting against a key nothing in production uses, and SC-013 unmeasurable while reading zero. The structural test (test 5) is therefore **repo-wide including `_test.go` files**, with exactly one allowed exception: the migration helper's own doc comment.
3. **Semantics unmodified, not text unmodified.** §10.1's regression bar for these files is now: *the session-id argument is mechanically re-pointed at the browsing key; no assertion is weakened, deleted, skipped or `t.Skip`-guarded; the test count per file is unchanged or higher.* A diff that changes anything else in these files is a finding.
4. **Ordering:** the migration lands in the **same commit** as FR-002b's deletion. A commit that deletes the constant and leaves the tests for a follow-up leaves `pkg/tools/browser` and `pkg/gateway` unbuildable, so CI's verdict on every other gate becomes meaningless.

Effort, stated rather than implied: ~364 mechanical edits across 25 files, of which `tabs_test.go` alone is 100. This is the single largest line-count item in the change and it belongs to **Stream A**, not to Stream G.

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

Seven streams (A, P, C, D, E, F, G). **Stream A is the critical path and must land its interface first**; the rest code against it. **Stream P (the pool) is gated on G-1 and G-2 (§0.3)** and is the largest.

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
//
// The "ws:" prefix is RETAINED DELIBERATELY, and not as future-proofing for a
// second shape — §5 forbids adding one (round-2 MIN-103). It is a namespace
// marker: a workspace id is a bare ULID, and a bare ULID appearing in an audit
// event, a WARN line or a LiveKeys() dump is indistinguishable from a session
// id, a task id or an agent id, all of which are ULIDs in this system and all
// of which appear in the same log lines. "ws:01J..." is self-describing at the
// point a human reads it, which is the only place it matters. WorkspaceID()
// exists for the two consumers that need the bare id back — the profile
// directory path (FR-037) and the audit event's workspace field (FR-027).
type BrowsingKey struct{ s string }

func (k BrowsingKey) String() string { return k.s }
func (k BrowsingKey) IsZero() bool   { return k.s == "" }

// WorkspaceID returns the workspace this key names, without the prefix. Used
// by audit and by the profile-directory path; never a branch in isolation
// logic. Its result is a path segment (FR-037), so §5's path-segment
// invariant applies to it.
func (k BrowsingKey) WorkspaceID() string

// ErrNoBrowsingContext is the D1.4 named failure. It MUST be returned — never
// swallowed into a shared browser, never mapped to a constant, never
// nil-with-empty. Its Error() text is a behavioural contract (FR-008).
var ErrNoBrowsingContext = errors.New(
    "browser: this turn is not rooted in a workspace, so it has no browser of its own; " +
        "add this agent to a workspace's team, or run the request in a workspace chat")

// errBrowserPoolFull + errPoolFull are the FR-039 refusal. The pool NEVER evicts a live
// browser to make room — a silent eviction logs someone out mid-task, which is
// strictly worse than a refusal that names the cap.
//
// THE REMEDY MUST BE ONE THE OPERATOR CAN PERFORM (round-2 CRIT-103). The
// previous text said "close a browser panel or finish work in another
// workspace" and NEITHER action frees a slot: closing a panel only detaches a
// viewer (ViewerDetached), and FR-040's idle close needs zero tabs AND zero
// viewers past the idle window, so a workspace with a tab open never closes no
// matter how many panels shut; "finish work in another workspace" has no
// mechanism behind it at all. With N workspaces holding tabs the (N+1)th was
// refused permanently and told to do something ineffective. Both actions named
// below are real: FR-046 adds the explicit close, and max_browsers is
// reload-applied without a restart (FR-038).
//
// It is a formatted error, not a bare sentinel, because the cap's value is the
// single most useful thing in the message. errors.Is against errBrowserPoolFull
// is how callers test it (FR-039).
func errPoolFull(cap int) error {
    return fmt.Errorf("%w: the maximum number of concurrent workspace browsers is already open "+
        "(tools.browser.max_browsers=%d). Close another workspace's browser "+
        "(Workspaces -> that workspace -> Browser -> Close, or "+
        "POST /api/v1/workspaces/{id}/browser/close), or raise "+
        "tools.browser.max_browsers in Settings (it applies without a restart), then retry",
        errBrowserPoolFull, cap)
}

var errBrowserPoolFull = errors.New("browser: browser pool full")
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
    // Returns ErrNoBrowsingContext, an errBrowserPoolFull-wrapping error, or a launch error.
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
// Returns an errBrowserPoolFull-wrapping error when the cap is reached (FR-039).
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
    BrowserResolvePoolFull      // errors.Is(err, errBrowserPoolFull)
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
// PATHS — corrected from the previous draft, which nested a user-data-dir
// inside a user-data-dir and broke the managed-Chromium install root
// (round-2 MAJ-103). cfg.ProfileDir KEEPS its current meaning: it is itself a
// Chrome user-data-dir (default ~/.omnipus/browser/profiles/default/,
// manager.go:125). Per-key profiles are therefore its SIBLINGS, under a
// profileRoot derived once at pool construction:
//
//   profileRoot   = filepath.Dir(cfg.ProfileDir)     // ~/.omnipus/browser/profiles
//   Profile dir:    <profileRoot>/ws/<workspaceID>/
//   Launch lock:    <profileRoot>/ws/<workspaceID>/chrome.lock
//   Ownership marker: $OMNIPUS_HOME/browser/ws-<workspaceID>.pid
//
// INVARIANT P-5 (subtle, and the reason profileRoot is not just the per-key
// dir): the managed-Chromium install root is computed by path arithmetic from
// a profile dir — InstallRootForProfileDir(p) = Clean(Join(Dir(p), "..",
// "chromium")) (exec_resolver.go:50). Feeding it a per-key dir gives
// <profileRoot>/chromium, a DIFFERENT and wrong location per key, so every
// workspace's Chrome would look for (and re-download) its own binary. The pool
// therefore resolves the executable ONCE, from cfg.ProfileDir, and hands the
// same path to every key. Never call InstallRootForProfileDir with a per-key
// path. (FR-037a; asserted by test 52.)
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

// Acquire returns the live Chrome for k, launching it if absent. Before the
// launch it runs cleanStaleSingletons against k's OWN profile dir (FR-042b) —
// the shipped call passes cfg.ProfileDir only (coordinator.go:1235), so
// without this a crash leaves a stale SingletonLock in every per-key profile
// and Chrome refuses to relaunch, which would make FR-043's "the profile
// survives so the login survives" false in the one case it exists for.
func (p *BrowserPool) Acquire(ctx context.Context, k BrowsingKey) (*chromeInstance, error)

// Close shuts down k's Chrome and releases its pool entry. Idempotent. The
// ONLY process-disposal path (FR-040, FR-026a, FR-026c, FR-046).
//
// It does NOT delete k's profile directory (§5, FR-043) and it does NOT remove
// k's browserMgrs entry or its BrowserManager — see the liveness distinction
// in FR-040a. Deleting the profile is a separate, narrower operation:
// DeleteProfile, called only on workspace deletion (FR-043a).
func (p *BrowserPool) Close(k BrowsingKey)

// CloseIdle closes every key whose Chrome has zero tabs and zero viewers and
// has been in that state for longer than idleCloseTTL. Called from the
// existing 1-minute idle sweep (gateway.go:5321-5352) AFTER its per-manager
// ReapIdleSessions loop, so the tabs a sweep reaps are already gone when the
// browser-level decision is made on the same tick (FR-040a). Returns the keys
// it closed, for the sweep's log line.
func (p *BrowserPool) CloseIdle(now time.Time) []BrowsingKey

// DeleteProfile removes k's profile directory from disk. Called ONLY on
// workspace deletion, after Close(k) has returned (FR-043a). Separate from
// Close precisely so that idle close, roster change, reload and crash recovery
// cannot reach it — the logins are the point of the profile.
func (p *BrowserPool) DeleteProfile(k BrowsingKey) error

// ReconcileMarkers runs ONCE at boot, before any Acquire, over
// $OMNIPUS_HOME/browser/ws-*.pid. With one marker there was one adoption story;
// with N, a kill -9'd gateway leaves N markers and N orphan Chromes that the
// in-memory cap cannot see, so the cap would bound only this process's Chromes
// and not the host's — defeating its stated purpose (FR-042a).
func (p *BrowserPool) ReconcileMarkers() (reclaimed, orphaned int)

// Preprovision resolves — and on a fresh install downloads — the managed
// Chromium binary, ONCE, at boot, with no live key and no launch. It is the
// pool-level replacement for gateway.go:2286's range over BrowserManagers(),
// which under a lazily-created pool iterates an EMPTY slice at boot and
// silently moves a multi-hundred-megabyte download to the first tool call
// (FR-016c). Safe to call with zero live keys; that is the normal case.
// BrowserManager.Preprovision's whole body is a config check plus
// resolveExecPath (manager.go:3218-3223), so nothing here needs a key.
func (p *BrowserPool) Preprovision(ctx context.Context) (string, error)

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
Owns `pool.go`; the coordinator's decomposition into per-key instances; per-key profile dirs, launch locks and ownership markers (FR-037, FR-037a, FR-042); the cap, its edge semantics and its refusal (FR-038, FR-038a, FR-039); whole-Chrome idle close with its config key, caller and post-close state (FR-040, FR-040a); boot marker reconciliation (FR-042a) and per-key stale-singleton cleanup (FR-042b); per-Chrome crash containment, replacing `watchForCrash`'s reset-everything behaviour (FR-041); profile-based reload survival replacing ADR-043 CRIT-002's context re-adoption (FR-043) and the profile's deletion path (FR-043a); boot preprovision decoupled from `BrowserManagers()` (FR-016c); retirement of `capture_shared_context`, `disposeBrowserContextRaw`, `contextCount()` and the CDP-context branch of `Register` (FR-031).
**Also owns FR-034a — the final description literals** (§3.3), which must land in the same commit as FR-037 and not before.
Depends on: A's key type. **Do not start before G-2 passes.**

**Stream C — Three-state tabs + descriptions (D1.5) [depends on A].**
Owns `ListTabsState` + `ListTabs` delegation (`manager.go:1605-1613`); `ListTabsTool.Execute` (`tabs.go:48-68`); the five model-visible strings, their **interim** replacement literals (FR-015, FR-034 — §3.3), and the two Go comments (`tabs.go:19,186`).
Does **not** own the "not permitted" state — that is the policy layer (FR-014, FR-014a).
**Does NOT own the final literals.** FR-034a's isolation-asserting text lands in Stream P's commit, not this one (§3.3, MAJ-107).

**Stream D — Write lease [depends on A].** Owns `lease.go` per **§14**, the call pairs in every mutating tool, and composition with `controlledResult`. **§14 is normative; this stream implements it and the D2 spec references it.**

**Stream E — Gateway resolution + contracts [depends on A].**
Owns the three `BrowserManagerForAgent` call sites; server-side agent→workspace resolution preferring the attaching session's `workspace_id` (`pkg/session/unified_meta_files.go:60`); the capture registry's re-keying and the ADR-048 conflict rule's collapse (FR-016a); the boot warm path (FR-016b); the panel's failure messages (FR-008a); the three schema description edits, **one of which is a semantic reversal and must be reviewed as one** (FR-016, MAJ-004 in §15); the **new** `POST /api/v1/workspaces/{id}/browser/close` path and its SPA control (FR-046); and the Workspace → Team elevation-of-privilege disclosure (FR-047).
**FR-046 and FR-047 do not depend on Stream P** and should land in §0.4: FR-047 is true today (adding an agent to a workspace already grants it that workspace's browser once Stream A lands), and FR-046 is a `pool.Close`/`coordinator` call that degrades to closing the single shared Chrome before P ships.

**Stream F — Audit + lifecycle [depends on A, P].**
Owns the audit events (FR-027) and their provenance assertion (FR-035); disposal on workspace deletion and roster change (FR-026); the reaper interactions (FR-025) and the pool's idle-close hook (FR-040); `#659`'s auto-deny requirement for delegated sub-turns (FR-032).

**Stream G — Tests + regression (cross-cutting).** Owns §10. **Does not own the 364-reference test migration** — that is Stream A's, in Stream A's own commits, because it is a compile dependency of FR-002b rather than a test-quality task (§2.2a).

**Parallelization:** A lands its interface first, with the §2.2a migration in the same commits. C/D/E/F fan out on disjoint files. P runs behind its gates and lands last.

**Why "last" is safe for the mechanism but not for the words (round-2 MAJ-107).** Until P lands the product has one Chrome and therefore one cookie partition — which is exactly today's behaviour, so the intermediate state is not a *behavioural* regression. The previous draft stopped there and used that to justify shipping the description corrections in Stream C. **That was wrong**, because FR-034's replacement literal asserted the browser is *"shared across this workspace"* — a sentence whose implicature is that it is **not** shared beyond it, while `capture_shared_context: true` (`defaults.go:671`) still means one partition across **all** workspaces until P lands. That is a false ownership claim made to the model and shown to the operator: the exact defect class ADR-072 §1.1 exists to fix, reintroduced by the fix for it.

The split in §3.3 removes the problem rather than accepting it: Stream C's literals assert only tab-set sharing (true after Stream A), Stream P's assert isolation (true only after FR-037), and §5 records the general rule so a future edit cannot re-merge them.

**And if G-2 fails after §0.4 has landed** (round-2's unasked question 10): the rollback is now nothing, because nothing shipped in §0.4 asserts isolation. Stream C's interim literals stay true whether or not the pool is ever built. That is the reason the split is worth its cost.

### 3.3 The description literals (FR-015, FR-034, FR-034a) — specified, in two stages

The previous draft's FR-034 said the replacement literals were "**specified here**" and then specified none of them anywhere in the document. They are below. Test 9 asserts the exact strings.

**The five model-visible occurrences of "the shared browser session"** — `tabs.go:32` (`browser_list_tabs`), `:86` (`browser_switch_tab`), `:143` (`browser_close_tab`), `:206` (`browser_open_tab`), and `tools.go:415` (a parameter description). Plus two Go comments (`tabs.go:19,186`), which are not model-visible and change with the code.

| Stage | Lands with | Replacement for "the shared browser session" | What it claims, and why that is true at that point |
|---|---|---|---|
| **Interim (FR-034)** | Stream C, §0.4 | **"the browser this workspace's agents share"** | Claims only that every agent on the workspace addresses one tab set — which is exactly what Stream A's `ManagerResolver` makes true, and is the reported defect's fix. Says nothing about other workspaces, so it cannot be false while one Chrome is still shared process-wide. |
| **Final (FR-034a)** | Stream P, same commit as FR-037 | **"this workspace's browser"**, plus one added sentence in `browser_list_tabs`' and `browser_open_tab`' descriptions: **"Each workspace has its own browser, with its own logins; you cannot see or use another workspace's."** | Claims isolation. True only once FR-037 gives each workspace its own Chrome process and profile directory. |

The parameter description at `tools.go:415` takes the interim form at both stages — a parameter doc is the wrong place for a scoping claim, and it is the one occurrence where "the browser this workspace's agents share" reads correctly in both worlds.

**Test 9 runs twice with different expectations,** and this is deliberate rather than a table-driven convenience: `TestToolDescriptions_NoFalseSharedClaim` asserts (a) the phrase "the shared browser session" appears zero times in `pkg/tools/browser` after Stream C, and (b) the stage-appropriate literal is present verbatim. Its stage-P form additionally asserts the isolation sentence is **absent** before FR-037's commit — an assertion that can only be written as a build-tag-free check of the two literals against the presence of `pool.go`, so it is stated plainly in §10 as an ordering requirement the reviewer checks rather than a test that can enforce its own ordering.

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
17. **Pool cap.** When the cap is reached, a request for a **new** workspace's browser is **refused** with an error satisfying `errors.Is(err, errBrowserPoolFull)` whose text names the cap's value and two remedies that work (FR-039, FR-046); no live browser is closed to make room, and no user is logged out to serve someone else.
18. **Crash containment.** One workspace's Chrome dying leaves every other workspace's browsing unaffected — its tabs, its panel and its logins survive.
19. **Idle close.** A workspace browser with no tabs and no viewers for longer than `tools.browser.idle_close_ttl` (default 15 minutes) is closed entirely, releasing its process; its profile — and therefore its logins — remains on disk. The next tool call for that workspace relaunches Chrome from that profile and is **still logged in**.
20. **Unattended work cannot hang.** An `ask`-policy tool reached from a delegated sub-turn is **denied**, not queued against an operator who is not there (FR-032, #659).
21. **A full pool has a way out.** An operator refused at the cap can close a named workspace's browser from the UI (or one REST call) and immediately retry, or raise the cap without restarting the gateway. The refusal message names both.
22. **A crashed gateway leaves nothing behind.** After a `kill -9`, the next boot leaves zero orphan Chromes and zero stale ownership markers under `$OMNIPUS_HOME`, and every workspace's next browser call relaunches cleanly from its own profile — no stale `SingletonLock` refusal.
23. **Deleting a workspace deletes its logins.** Deleting a workspace closes its Chrome **and removes its profile directory**. The client's cookies and tokens do not outlive the workspace. Idle close, roster change, reload and crash recovery never delete a profile.
24. **Adding an agent to a team says what it grants.** The Workspace → Team UI states, at the point of adding, that the agent gains every live browser session on that workspace — including on turns nobody is watching.

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
- The system must **not** destroy a browser on hot reload. Only workspace deletion, roster change, idle close, the operator's explicit close (FR-046), or gateway `Close()` (FR-026a, FR-040).
- The system must **not** delete a workspace's **profile directory** when its Chrome is closed for idleness, for a roster change, for a reload, for a crash, or by the operator's explicit close — the logins are the point of the profile (FR-040, FR-043). **Workspace deletion is the sole exception and it MUST delete** (FR-043a): a departed client's cookies and tokens must not outlive the workspace that named them.
- The system must **not** interpolate a workspace id into a filesystem path without validating it as a single path segment. It happens to be safe today — ids are server-minted ULIDs (`rest_workspaces.go:495` for the default workspace, `:848` for created ones) — but the path depends on a property nothing records, so a future id-format change (an operator-chosen slug, an import, a migration) would silently turn `<profileRoot>/ws/<id>/` into a path-traversal surface. The invariant is written down and enforced: `filepath.Base(id) == id` and `id != "." && id != ".."`, checked in `ResolveBrowsingKey` before the key is constructed, with the refusal treated as `ErrNoBrowsingContext` (FR-037, round-2 MIN-106).
- The system must **not** ship a model-visible description that asserts cross-workspace isolation before FR-037 lands. A description is a claim to the model and to the operator, and shipping the claim ahead of the behaviour is the defect ADR-072 §1.1 exists to fix, not a harmless ordering (§3.3, round-2 MAJ-107). The general rule: **no description may assert a property the current commit does not implement**, even when the wrong intermediate behaviour is identical to today's.
- The system must **not** compute the managed-Chromium install root from a per-key profile directory. `InstallRootForProfileDir` (`exec_resolver.go:50`) is path arithmetic over the *parent* of what it is given, so a per-key path yields a different, wrong install root per workspace and N downloads of the same binary (FR-037a).
- The system must **not** let the browser cap bound only this process's Chromes. Orphans from a crashed gateway consume the same host memory the cap exists to bound, so they are reconciled at boot rather than ignored (FR-042a).
- The system must **not** return `nil, 0, nil` from `ListTabs` for a missing browser once `ListTabsState` exists.
- The system must **not** add, remove or retype any field in an **existing** `contracts/` schema for D1. Descriptions change, and **one of those description changes is a semantic reversal** that must be reviewed as a behavioural contract change, not as prose (FR-016, §15 MAJ-004). **One exception, added by FR-046 and deliberately narrow:** a single new REST **path**, `POST /api/v1/workspaces/{id}/browser/close`, is added to `contracts/openapi.yaml` with a 204 response and no request or response body — so no schema file is added and no existing schema is touched. It follows Hard Constraint #8's 5-step process like any other wire change, and SC-007's condition (2) is amended from "no `contracts/` diff outside `description:`" to "no `properties:`/`required:`/`enum:`/`type:` change in any existing schema, and exactly one added path".
- The system must **not** widen the seeded browser tool policy. Mia and Ava stay denied.
- The system must **not** hold `m.mu` or `pool.mu` across `acquireWrite`, a Chrome launch, or any CDP call.
- The system must **not** change the `browser-webrtc[<agent>]` log label to a workspace label — cosmetic, and the agent is still the useful identity in a log line (round-1 review O3).
- The system must **not** re-key the `serve_web` preview URL. Out of scope, ADR §6 open.
- The system must **not** ship a `max_browsers` default derived from an estimate (FR-044).

---

## 6. Integration boundaries

- **Chrome processes / CDP.** The count of live Chromes now scales with **workspaces being actively browsed**, bounded by `max_browsers` (FR-038). Each is launched over the pipe transport (`exec_resolver.go`, `cdppipe`) with its own `--user-data-dir`. A launch failure surfaces as a tool error naming the workspace — never a silent join to another workspace's browser. **CDP browser contexts are no longer created at all** (FR-031).
- **Sandbox (Landlock/seccomp).** No new network surface: CDP flows over inherited fds 3/4, and the fixed DevTools port allow-rule was already removed (`pkg/gateway/sandbox_apply.go:412-417`). What *is* new is **N profile directories**, so the filesystem allow-list must cover `<profileRoot>/ws/` as a subtree rather than a single profile path (`profileRoot = filepath.Dir(cfg.ProfileDir)`; §3.1). Verify against `sandbox_apply.go`'s path rules before the pool lands — this is the one sandbox interaction that is genuinely new.
- **Host memory.** The binding cost, and the reason for the cap. G-1 (FR-044) measures it; ADR-043's "≈4–5 GB at ten" is unmeasured and per-agent, so it must not be quoted as the figure.
- **Workspace store** (`pkg/workspace/find_for_agent.go`): read-only. `FindForAgent` tie-breaks by sorted-first id (`:45-48`); `FindForAgentPreferring`'s fast path suppresses the ambiguity WARN (`:168-176`). FR-033 declines that tie-break for browsing keys and requires the WARN on **both** paths whenever it would have arbitrated.
- **Fresh install.** A fresh install is **not** workspace-less: `ensureDefaultWorkspace` (`rest_workspaces.go:468`, called at `gateway.go:5013` on every boot) creates "My Workspace" with `defaultWorkspaceTeam(cfg)` = `coreagent.All() ∩ configured agents` (`rest_workspace_delegation.go:359-379`), which includes Jim and Ray — the two browser-policy-allowed agents. So the default path resolves. **The residual case is a custom agent**: the system deliberately never auto-adds a custom/pre-existing agent to any workspace team (ADR-046 FR-008, stated at `gateway.go:5018-5025`), so a custom browser-allowed agent resolves to nothing and must be told why (FR-008a, US-14). **That condition already has a shipped boot-time surface and US-14 reuses it rather than inventing a second vocabulary** (round-2 MIN-107): `logWorkspacelessAgents(homePath, cfg)` (`gateway.go:5026`, immediately after `ensureDefaultWorkspace` at `:5013`) exists precisely to list, once at boot, the agents that *"silently cannot execute at all until manually added via a workspace's Team tab"* (`gateway.go:5015-5025`). FR-008a's panel reason and `ErrNoBrowsingContext`'s text must name the **same** remedy in the **same** words as that log line, so an operator who sees both does not think they are two problems.
- **Host memory and orphan Chromes.** The shipped ownership marker is consulted at **launch** time, not at boot: `acquireLaunchLockWithMarker` (`coordinator.go:1448-1482`) reads the marker and, if its pid is alive and owned by omnipus, **refuses to launch** with a named error rather than adopting or killing; if the pid is dead it clears the stale lock and retries. That is a reasonable single-Chrome story and it is not a boot-time story. With N markers it leaves N orphan Chromes consuming host memory that `LiveKeys()` cannot see, so the cap would bound this *process's* Chromes and not the *host's* — which is the only thing the cap is for. FR-042a adds the boot pass; §12 A20 records the kill-vs-warn trade-off it makes.
- **Session store** (`pkg/session/unified_meta_files.go:60`): the gateway reads `workspace_id` off the attaching chat session's meta. A session without one degrades to `FindForAgentPreferring(home, agentID, "")` — same ladder, same FR-033 refusal on ambiguity.
- **Scheduled and heartbeat turns already carry a workspace, and the previous draft did not know it** (round-2 MAJ-113). Rung 2 and FR-033's refusal were designed against the premise that these turns arrive with an empty `ToolWorkspaceID`. **The shipped code contradicts that as the normal case for the turns that matter most.** `pickSession` (`pkg/gateway/schedules.go:490`, called on every fire at `:141`) resolves the job's workspace via `resolveScheduleWorkspaceID` (`:581-639`) and stamps it onto the session meta *before* the run; `ProcessScheduled` then reads it back (`loop.go:6934-6946`) into `processOptions.WorkspaceID` (`:6957`). **Heartbeats are workspace-scoped by construction:** the reconciler names each job `heartbeat:<workspaceID>:<agentID>` (`heartbeat_schedule.go:30-33`) and that `(workspace, agent)` pairing never changes for the job's lifetime; `workspaceIDFromHeartbeatJobName` (`schedules.go:654`) parses it back. **So a heartbeat turn resolves at rung 1 and never reaches FR-033.** The round-2 review's stated consequence — *"the first time an operator adds Ray to a second workspace, every Ray heartbeat permanently loses the browser"* — **is false**: enabling a heartbeat on a second workspace creates a *distinct* job with a distinct name and its own workspace, so each of Ray's heartbeats resolves to its own workspace deterministically. See §16 MAJ-113. **What is left, and it is real:** a *plain, operator-created* schedule resolves to `""` — `resolveScheduleWorkspaceID` returns only the heartbeat-name parse, because ADR-065 FR-8 removed the channel plumbing that used to be its second source (`schedules.go:632-639`). So a plain schedule owned by an agent on two or more workspaces **does** hit rung 2 and **is** refused by FR-033. That case is narrow, it is the case where "which client's logins?" genuinely has no answer, and refusing it is the ruling FR-033 already makes. §12 A19 records the alternative (a per-agent browsing-home workspace) as considered and declined, with the reason.
- **Lease wait vs the action-tool timeout** (`manager.go:35`, `:123`; `config.go:3632`): §14.1 required `leaseWaitTimeout` to be strictly less than "the shortest action-tool timeout" and never named it. It is `BrowserConfig.PageTimeout`, default **30s**, operator-settable as **`tools.browser.page_timeout`** (JSON `page_timeout`, field `PageTimeoutSec`, env `OMNIPUS_TOOLS_BROWSER_PAGE_TIMEOUT`, applied at `loop.go:2311-2312`). *(The round-2 review called this key `page_timeout_sec`; that is the Go field's suffix, not the config key — §16 MIN-109.)* Both values are operator-configurable and **nothing validates the relationship**, so an operator can set `lease_wait` above `page_timeout` and turn every contended call into a CDP timeout **error** where FR-020 requires a non-error deferral. FR-023a adds the validation.
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
- **AC0 (corrected premise — round-2 MAJ-113): Given** a **heartbeat** turn, **Then** `ToolWorkspaceID(ctx)` is **not** empty and rung 1 resolves it. Heartbeat jobs are named `heartbeat:<workspaceID>:<agentID>` and that workspace is stamped onto the session meta before the run (`schedules.go:581-639` → `loop.go:6934-6957`), so a heartbeat never reaches rung 2 and an agent on several workspaces gets one deterministic browser per heartbeat job. The previous draft designed rung 2 against the belief that these turns arrive bare; they do not, and the requirement is asserted here so a future change to the stamping path fails this AC rather than silently re-routing every heartbeat through the ambiguity refusal.
- **AC1: Given** a turn with `ToolWorkspaceID(ctx) == ""` — in practice a **plain, operator-created** schedule, which resolves to `""` because ADR-065 FR-8 removed `resolveScheduleWorkspaceID`'s channel source (`schedules.go:632-639`) — whose work dir was re-rooted into workspace W, **When** it calls a browser tool, **Then** it reaches W's browser — the same id `FindForAgentPreferring` gave `resolvepath.go:713`.
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
- **AC4:** exempt tools are never deferred. The exempt set is **six** (§14 rule 3 is the normative count): four read-only tools shipped today — `browser_screenshot`, `browser_get_text`, `browser_wait`, **`browser_list_tabs`** — plus `browser_snapshot` (read-only, D2 FR-018) and `browser_handle_dialog` (**recovery** — D2 FR-035).
- **AC4a (the omission that made the headline demo defer):** `browser_list_tabs` is exempt. It is registered (`register.go:76`), it is read-only, and its own file says so (`tabs.go:20`: *"Read-only — NOT gated by controlledResult"*). The previous draft's closed set of five put it in neither category, so under AC5 it would have taken the **write** lease — making Jim's `browser_list_tabs`, the literal call in behavioural contract 1, US-1/AC1 and the headline BDD scenario, return `{"deferred": true}` whenever another agent held the lease for a long `browser_navigate`. **The feature's headline demo would have deferred behind an unrelated agent.**
- **AC5:** a `browser_*` tool takes the write lease **if and only if** it is gated by the ADR-038 D6 human-control lock (`controlledResult`). That biconditional — not a hand-written list — is the rule, and it holds exactly over the eleven tools shipped today (§14 rule 3's table). A tool that is leased but not control-gated, or control-gated but not leased, fails the gate (FR-019a).

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
- **AC4:** a workspace browser with zero tabs and zero viewers for longer than `tools.browser.idle_close_ttl` is **closed entirely** (process gone, `LiveKeys()` shrinks) while its **profile directory survives** on disk.
- **AC4a (the state the previous draft never described):** after AC4's close, the workspace's `browserMgrs` entry and its `*BrowserManager` **still exist** — FR-026a's liveness predicate says the *key* is live while the workspace exists, and that is deliberately a different question from whether a *Chrome process* is running. **When** a tool call for that workspace arrives, **Then** `pool.Acquire` relaunches Chrome from the surviving profile, `LiveKeys()` grows by one, and the session that was logged in **is still logged in**. No error, no second manager, no re-registration.
- **AC5:** running K delegated sub-turns to completion returns `len(LiveKeys())` and the manager count to their pre-run values — sub-turns create no browser of their own (US-5/AC2) and therefore leak none.
- **AC6:** the reaper and the pool do not fight. `ReapIdleSessions` already cancels a session's `browserCancel` and calls `coord.ReleaseTab` in its own removal branches (`manager.go:3027-3032`, `:3073-3078`, `:3118`, `:3123-3125`), so a sweep can leave a manager whose browsing context is cancelled while the pool still lists that key as live. **Given** that state, **Then** the next `Acquire` for the key produces a working browser rather than a live-but-undrivable one, and `LiveKeys()` never counts a Chrome nothing can drive (FR-040a).

**US-13 (P1) Repudiation.** As an operator, I can answer "which agent acted as the signed-in user".
- **AC1:** a browser's creation records key, workspace and establishing agent.
- **AC2:** an agent's **first** action in a browser it did not establish records agent, key and workspace. Subsequent actions by that same agent are not re-recorded — accepted, and stated so it is not mistaken for full action-level audit.

**US-14 (P1) An agent with no workspace is told the truth.** As an operator, when the browser will not work I learn *why*.
- *Context:* a fresh install **is** covered — `ensureDefaultWorkspace` seeds "My Workspace" with Jim and Ray (§6). The gap is a **custom** agent, which is deliberately never auto-added to a team.
- **AC1: Given** a custom browser-allowed agent on no workspace, **When** it calls a browser tool, **Then** the error is `ErrNoBrowsingContext`'s text, which names the remedy ("add this agent to a workspace's team").
- **AC2: Given** the same agent, **When** the operator attaches the live panel, **Then** the panel shows a reason distinguishing *"this agent is not on a workspace"* from *"browser tools are not registered for this agent"* — today both render as the latter (`browser_inspect.go:75-77`, `browser_ws.go:1252-1262`). (FR-008a.)
- **AC3:** the pool-full and ambiguous cases each render their own distinct reason (`BrowserResolveOutcome`).

**US-15 (P0) The pool is bounded, never logs anyone out to make room, and can always be unblocked.** As an operator on a sized host, opening an eleventh workspace must not close my tenth — and must not leave me stuck.
- **AC1: Given** `max_browsers = N` and N live browsers, **When** a turn resolves to an (N+1)th workspace, **Then** it fails with the pool-full text and **no** live browser is closed.
- **AC2: Given** the same, **Then** the N live browsers' pids are unchanged and no session cookie anywhere was lost.
- **AC3:** `max_browsers`' shipped default is derived from the G-1 measurement and the measurement is recorded in the PR (FR-044).
- **AC4 (the remedy is real): Given** the cap is reached **and every one of the N browsers has open tabs and an attached viewer** — so idle close can never fire for any of them — **When** the operator performs the action the refusal names, **Then** a slot is freed and the retry succeeds. The action is FR-046's explicit close, or raising `max_browsers` (which applies on reload, no restart). *This is the case the previous refusal text could not answer: it told the operator to close a panel, which only detaches a viewer, and idle close requires zero tabs **and** zero viewers.*
- **AC5: Given** `max_browsers <= 0`, **Then** the cap is **unlimited** — matching `max_total_tabs`' shipped semantics (`coordinator.go:785-788`) — and no request is ever refused on this axis. The shipped *default* is nevertheless a positive measured integer, and FR-038a states why the two keys share a shape but not a default.
- **AC6: Given** a configured `max_total_tabs`, **Then** it stays a **global** budget across all N Chromes, not per-Chrome. An operator who set a ceiling of 30 tabs gets 30 tabs, not 30 × N.

**US-18 (P0) The operator can close a workspace's browser.** As an operator, I can shut a workspace's browser without deleting the workspace and without restarting anything.
- *Why P0:* it is the only mechanism that frees a pool slot while people are working, so US-15/AC4 has nothing behind it otherwise.
- **AC1: Given** workspace W has a live browser with tabs and an attached viewer, **When** the operator invokes Close, **Then** W's Chrome exits, `LiveKeys()` shrinks by one, and the pool has a free slot.
- **AC2: Given** the same, **Then** W's **profile directory survives** — reopening W's browser is still logged in. Close is not deletion.
- **AC3: Given** an attached viewer, **When** Close runs, **Then** that viewer receives a `browser_status` error frame naming the operator close as the reason — never a silent dead stream.
- **AC4: Given** W has **no** live browser, **When** Close is invoked, **Then** it succeeds (204) rather than erroring. Idempotent.
- **AC5:** the action requires the same authorisation as any other workspace mutation, and is refused with 503 under `dev_mode_bypass` like other high-blast-radius admin routes (`RequireNotBypass`).

**US-19 (P1) A crashed gateway leaves nothing behind.** As an operator who `kill -9`'d the gateway (or whose host lost power), the next start is clean.
- **AC1: Given** three stale `$OMNIPUS_HOME/browser/ws-*.pid` markers whose pids are dead, **When** the gateway boots, **Then** all three markers are removed, their stale per-key launch locks are cleared, and one INFO line reports the count.
- **AC2: Given** a stale marker whose pid is **alive** and is a Chrome this install launched, **When** the gateway boots, **Then** that process is terminated and its marker removed, with a WARN naming the workspace and pid — so it cannot consume host memory outside the cap.
- **AC3: Given** workspace W's profile directory contains a stale `SingletonLock` from an ungraceful exit, **When** W's next browser tool call runs, **Then** Chrome launches successfully from that profile and the login is intact. *Without this, FR-043's promise fails in exactly the case it exists for.*

**US-20 (P1) A departed client's logins depart with them.** As an operator who deletes a client's workspace, I can answer "are their logins gone?" with yes.
- *Why P1 but security-relevant:* ADR D2.11's data-at-rest case; the profile holds session cookies and tokens for a named third party.
- **AC1: Given** workspace W has a browser with a live login, **When** W is deleted, **Then** W's Chrome closes **and** `<profileRoot>/ws/<W>/` no longer exists on disk.
- **AC2: Given** the same, **Then** deletion of the directory happens only after `pool.Close(ws:W)` returns, so no Chrome is writing into a directory being removed.
- **AC3:** idle close, roster change, reload, operator close and crash recovery each leave the profile directory **present**. Only workspace deletion removes it.
- **AC4:** per-key profile directories are created `0700`, matching the mode the shipped code already uses for profile dirs (`coordinator.go:1232`, `manager.go:799`) — stated rather than inherited, because these now hold per-client session cookies.

**US-21 (P1) Adding an agent to a team says what it grants.** As an operator, I learn that adding an agent to a workspace hands it that workspace's live logins **before** I add it, not in a release note afterwards.
- *Why:* ADR D2.11's elevation-of-privilege decision — *"the team-editing UI must state this at the point of adding, not only in release notes"* — which §1's out-of-scope wording left in this spec's scope and which no spec had claimed (round-2 MAJ-114). D1.2 makes it worse than when the ADR wrote it: unattended delegated work now inherits those logins too.
- **AC1: Given** the Workspace → Team editing surface, **When** the operator opens the add-agent control, **Then** the disclosure is visible **before** confirming — not in a tooltip, not only after the fact.
- **AC2:** the text names the concrete consequence, not the mechanism: that the agent will be able to act as whoever this workspace is signed in as, on any site it is signed into, including on turns nobody is watching.
- **AC3:** the same disclosure appears in the release note for this change.

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

**Scenario: read-only tools are never deferred, including list_tabs — US-9/AC4+AC4a, FR-021**
- **Given** Jim holds the write lease on W's browser for a long navigation
- **When** Ray calls `browser_screenshot`, `browser_get_text`, `browser_wait` and **`browser_list_tabs`**
- **Then** all four execute; none returns a `deferred` body
- **And** in particular `browser_list_tabs` returns the current tab set rather than deferring — the handover answer must not queue behind an unrelated agent's navigation

**Scenario: lease membership follows the control-lock gate, not a list — US-9/AC5, FR-019a**
- **Given** the tool registry after `RegisterTools`
- **When** each registered `browser_*` tool is exercised twice: once with a human holding the control lock, once with another agent holding the write lease
- **Then** for every tool the two answers agree — it defers under both, or under neither
- **And** the tools that defer under both are exactly `{browser_navigate, browser_click, browser_type, browser_evaluate, browser_switch_tab, browser_close_tab, browser_open_tab}`
- **And** the tools that defer under neither are exactly `{browser_screenshot, browser_get_text, browser_wait, browser_list_tabs}`
- **And** no registered `browser_*` tool falls outside those two sets

**Scenario: the pool refuses at the cap rather than evicting — US-15/AC1+AC2, FR-038, FR-039**
- **Given** `max_browsers = 2` and live browsers for workspaces W1 and W2, W1 holding a login
- **When** a turn resolves to workspace W3
- **Then** the tool result is an error satisfying `errors.Is(err, errBrowserPoolFull)`, whose text names the cap's configured value
- **And** `pool.PID(ws:W1)` and `pool.PID(ws:W2)` are unchanged and W1's cookie is still present

**Scenario: the cap's edge values match its sibling key — US-15/AC5+AC6, FR-038a**
- **Given** `max_browsers = 0` (and separately, `-1`)
- **When** turns resolve to five different workspaces
- **Then** all five get a browser — 0 and negative mean **unlimited**, exactly as `max_total_tabs` behaves (`coordinator.go:785-788`)
- **And given** a configured `max_total_tabs = 3` with browsers live for W1 and W2, **then** the tab budget is still **global**: the third tab opened across both browsers is the last one allowed, not the third in each

**Scenario: the managed-Chromium download still starts at boot — FR-016c**
- **Given** a fresh install with no Chromium on `$PATH` and no managed install, and **zero** live browsing keys
- **When** the gateway boots
- **Then** the managed-Chromium resolution/download has started, in the background, before any browser tool call
- **And** it started without any `BrowserManager` existing — the previous boot path ranged `BrowserManagers()`, which is empty at boot under a lazy pool

**Scenario: one Chrome crash is one workspace — US-16, FR-041**
- **Given** live browsers for W1 and W2, each with an attached viewer
- **When** W1's Chrome process is killed
- **Then** `pool.PID(ws:W2)` is unchanged, W2's tabs and viewer stream survive, and W2's manager was not reset
- **And when** W1's next browser tool runs, **then** W1's Chrome relaunches from W1's profile dir and W1's login is still present

**Scenario: idle close frees the process but keeps the profile — US-12/AC4, FR-040**
- **Given** workspace W's browser has zero tabs and zero viewers, and `tools.browser.idle_close_ttl` has elapsed since it reached that state
- **When** the 1-minute sweep runs (`gateway.go:5321-5352`, after its per-manager `ReapIdleSessions` loop)
- **Then** W's Chrome process is gone and `pool.LiveKeys()` no longer contains `ws:W`
- **And** W's profile directory still exists on disk

**Scenario: an idle-closed workspace relaunches, still logged in — US-12/AC4a, FR-040a**
- **Given** `ws:W` was idle-closed and its `browserMgrs` entry and `*BrowserManager` still exist
- **When** an agent on W calls `browser_navigate`
- **Then** `pool.Acquire(ws:W)` relaunches Chrome from `<profileRoot>/ws/W/`, `LiveKeys()` grows by one, and the site is **still logged in**
- **And** no second `*BrowserManager` was created and no re-registration occurred
- **And** the relaunch succeeded even though the previous exit left a `SingletonLock` in that profile — `cleanStaleSingletons` ran against W's own directory, not just `cfg.ProfileDir`

**Scenario: the reaper cancels a browser context while the pool still lists the key (Edge Case) — US-12/AC6, FR-040a**
- **Given** `ws:W` is live in the pool and its manager's session hits `ReapIdleSessions`' all-tabs-idle branch, which cancels `se.browserCancel` (`manager.go:3073-3078`, `:3123-3125`) and calls `releaseGlobalTab` → `coord.ReleaseTab`
- **When** the next browser tool call for `ws:W` runs
- **Then** it gets a working browser — not a live-but-undrivable one
- **And** `pool.LiveKeys()` never reports a key whose Chrome cannot be driven

**Scenario: the pool refuses when every browser is pinned, and the named remedy works (Error) — US-15/AC1+AC4, FR-039, FR-046**
- **Given** `max_browsers = 2`, live browsers for W1 and W2, **each with an open tab and an attached viewer** — so idle close can never fire for either
- **When** a turn resolves to W3
- **Then** it is refused, the message names `tools.browser.max_browsers`, its current value, the close action and the reload-without-restart raise
- **And when** the operator closes W2's browser via `POST /api/v1/workspaces/W2/browser/close`
- **Then** W2's Chrome exits, W2's viewer receives a `browser_status` error naming the operator close, W2's profile directory survives
- **And** the retry for W3 now succeeds

**Scenario: close is not deletion — US-18/AC2+AC4, FR-046**
- **Given** workspace W's browser holds a login and the operator closes it
- **When** W's browser is opened again
- **Then** the site is still logged in
- **And when** Close is invoked a second time with no live browser, **then** it returns 204 rather than an error

**Scenario: a crashed gateway leaves no orphans — US-19/AC1+AC2, FR-042a**
- **Given** three `$OMNIPUS_HOME/browser/ws-*.pid` markers survive a `kill -9`: two whose pids are dead, one whose Chrome is still running
- **When** the gateway boots
- **Then** all three markers are gone, the two stale per-key launch locks are cleared, the surviving Chrome has been terminated
- **And** `len(pool.LiveKeys())` is 0 and no Chrome from the previous run remains on the host
- **And** one INFO names the reclaimed count and one WARN names the terminated workspace and pid

**Scenario: deleting a workspace deletes its logins — US-20, FR-043a**
- **Given** workspace W has a live browser with a session cookie on `example.com`
- **When** W is deleted
- **Then** `pool.Close(ws:W)` is called exactly once **and returns before** `<profileRoot>/ws/W/` is removed
- **And** `<profileRoot>/ws/W/` no longer exists
- **And given** instead that W was only idle-closed, or lost its last browser-allowed agent, or the operator closed it, **then** `<profileRoot>/ws/W/` still exists in all three cases

**Scenario: adding an agent to a team discloses what it grants — US-21, FR-047**
- **Given** workspace W is signed into a site and the operator opens Workspace → Team → add an agent
- **When** the add control is shown
- **Then** the disclosure is visible before confirming, and names the consequence (the agent can act as whoever W is signed in as, including on turns nobody is watching)
- **And** it is not deferred to a tooltip or to a post-hoc toast

**Scenario: lease_wait cannot exceed the action-tool timeout (Error) — FR-023a**
- **Given** a config with `tools.browser.lease_wait = 45s` and `tools.browser.page_timeout = 30s`
- **When** the config loads, and again when it is hot-reloaded
- **Then** `leaseWaitTimeout` is clamped to at most half of `PageTimeout` and a WARN names both keys and both values
- **And** a contended action tool still returns a non-error `{"deferred": true, …}` rather than a CDP timeout error

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
| **FR-002b** | Every one of the 37 `DefaultSessionID` consumers (§2.2) addresses the resolved key; the constant and its alias are **deleted** | US-1, US-9 | human-outranks-lease | `TestNoResidualDefaultSessionID` (repo-wide structural, **including `_test.go`**) | CRIT-005 |
| **FR-002e** | The **364** test-side references across **25** files (§2.2a) are mechanically re-pointed at the browsing key **in the same commit as FR-002b's deletion**, with no alias in test code, no assertion weakened and no test count reduced | US-1 | — | test 5 (repo-wide incl. tests) + the §10.1 diff bar | round-2 CRIT-101/MIN-102 |
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
| **FR-034** | The **interim** replacement literals are specified verbatim in **§3.3** and claim only tab-set sharing; they land with Stream C | US-1 | — | test 9 (stage C), asserting the new literal | MIN-005 |
| **FR-034a** | The **final** literals (§3.3), which assert cross-workspace isolation, land in the **same commit as FR-037** and not before | US-3 | — (an ordering requirement; see the exemption table below) | test 9 (stage P) + the §3.3 ordering check | round-2 MAJ-107 |
| FR-016 | Gateway resolves agent→workspace server-side; no wire field added; the two reversed descriptions use FR-016's verbatim text | US-10 | wire-meaning-change-caught | `TestGateway_SessionIDIsBinding` | ADR-043 D3 / MAJ-004 |
| **FR-016a** | The capture registry is keyed by browsing key; **one capture session per workspace browser**; ADR-048's "requesting agent" conflict rule collapses | US-2 | human-browses-first | `TestCaptureRegistry_OnePerBrowsingKey` | MAJ-007 |
| **FR-016b** | Boot warm-tab warms the resolved workspace of the default agent; skipped with one INFO (not WARN) when nothing resolves | — | — | `TestPickWarmBrowser_UsesResolvedKey` | MAJ-006 |
| **FR-016c** | Boot **preprovision** is decoupled from `BrowserManagers()`: `pool.Preprovision(ctx)` resolves/downloads the managed Chromium once at boot with **zero live keys**, replacing `gateway.go:2286`'s range over a snapshot that is empty under a lazy pool | — | boot-preprovision | `TestPool_PreprovisionAtBootWithNoLiveKeys` | round-2 MAJ-104 |
| FR-017 | Gateway prefers the attaching session's `workspace_id` | US-2, US-11 | human-browses-first | `TestGateway_PrefersSessionWorkspaceID` | round-1 C4 |
| FR-018 | Multi-workspace agent: turn and panel agree, including agreeing to refuse | US-11 | ambiguous-refused | `TestMultiWorkspaceAgent_TurnAndPanelAgree` | §6 Q2 |
| FR-019 | Per-browser write lease held for one action-tool call (**§14**) | US-9 | two-writers | `TestWriteLease_OneWriterPerBrowser` | D2.10 |
| **FR-019a** | A `browser_*` tool takes the write lease **iff** it is gated by `controlledResult`; the exempt set is **six** (4 read-only shipped incl. `browser_list_tabs` + `browser_snapshot` + `browser_handle_dialog`); the check enumerates the **registry** and compares the two gates behaviourally | US-9/AC5 | lease-membership-follows-control-gate | `TestWriteLease_EveryActionToolIsLeased` | MAJ-008, round-2 CRIT-104/MAJ-101/MAJ-102 |
| **FR-023a** | `tools.browser.lease_wait` is **clamped** (never silently exceeded) against `tools.browser.page_timeout` at config load and on reload, with a WARN naming both keys and values | US-9 | lease-wait-clamped | `TestConfig_LeaseWaitClampedAgainstPageTimeout` | round-2 MAJ-112 |
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
| **FR-037** | One Chrome process and one `--user-data-dir` profile directory per browsing key, via `pipeLaunchConfig.userDataDir`; the workspace id is validated as a single path segment before it becomes one | US-3 | cross-workspace-isolation | `TestPool_OneChromePerKey`, `TestResolveBrowsingKey_RejectsNonSegmentWorkspaceID` | D1.1a, round-2 MIN-106 |
| **FR-037a** | Per-key profiles are **siblings** of `cfg.ProfileDir` under `profileRoot = filepath.Dir(cfg.ProfileDir)`, not children of it; `cfg.ProfileDir` keeps its current meaning; the managed-Chromium exec path is resolved **once** from `cfg.ProfileDir` and never via `InstallRootForProfileDir` on a per-key path | US-3 | — | `TestPool_InstallRootIsKeyIndependent` | round-2 MAJ-103 |
| **FR-038** | A configurable cap, `tools.browser.max_browsers`, on concurrently live Chromes; reload-applied without a restart | US-15 | pool-refuses-at-cap | `TestPool_CapIsEnforced` | D1.1a item 1 |
| **FR-038a** | Cap edge semantics: `<= 0` means **unlimited**, matching `max_total_tabs` (`coordinator.go:785-788`); the shipped **default** is nevertheless a positive measured integer (FR-044) and §12 A21 states why the two keys share a shape but not a default; `max_total_tabs` stays a **global** budget across all N Chromes, not per-Chrome | US-15/AC5+AC6 | cap-edge-values | `TestPool_ZeroAndNegativeCapAreUnlimited`, `TestPool_TabBudgetStaysGlobalAcrossChromes` | round-2 MAJ-109 |
| **FR-039** | At the cap the pool **refuses** (`errors.Is(err, errBrowserPoolFull)`); it never evicts a live browser; the message names the cap's value **and two actions that actually free a slot** — FR-046's close, and raising the cap on reload | US-15/AC1+AC2+AC4 | pool-refuses-when-all-pinned | `TestPool_RefusesNeverEvicts`, `TestPool_RefusalRemedyIsEffective` | D1.1a item 2, round-2 CRIT-103 |
| **FR-040** | Whole-Chrome idle close: zero tabs and zero viewers past `tools.browser.idle_close_ttl` closes the process; the **profile directory survives** | US-12/AC4 | idle-close-keeps-profile | `TestPool_IdleCloseKeepsProfile` | D1.1a item 3 |
| **FR-040a** | The idle window and the reaper↔pool contract are **named**: config key `tools.browser.idle_close_ttl` (default **15m** = 3× the per-tab `idle_ttl`; §12 A22 gives the derivation), caller = the existing 1-minute sweep (`gateway.go:5321-5352`) **after** its `ReapIdleSessions` loop, post-close state = pool entry and Chrome gone, `browserMgrs` entry and `*BrowserManager` **retained**, next call relaunches from the profile. `ReapIdleSessions` cancelling `se.browserCancel` must never leave a key the pool reports live but nothing can drive | US-12/AC4a+AC6 | idle-close-relaunch, reaper-cancels-while-pool-live | `TestPool_RelaunchAfterIdleClose`, `TestReaper_CancelDoesNotStrandPoolEntry` | round-2 CRIT-102, MAJ-108 |
| **FR-041** | Crash containment: one Chrome's death affects exactly one key; other keys' managers are not reset; recovery relaunches from the profile so the login survives | US-16 | one-crash-one-workspace | `TestPool_CrashIsContained` | D1.1a item 4 |
| **FR-042** | Per-key launch lock and ownership marker (`<profileRoot>/ws/<id>/chrome.lock`, `$OMNIPUS_HOME/browser/ws-<id>.pid`) replacing the singletons at `coordinator.go:1424,1527` | US-15 | — | `TestPool_PerKeyLockAndMarker` | D1.1a |
| **FR-042a** | **Boot reconciliation of the N markers.** Before any `Acquire`, scan `$OMNIPUS_HOME/browser/ws-*.pid`: dead pid ⇒ remove marker + stale per-key launch lock (INFO, with a count); live omnipus-owned pid ⇒ terminate it and remove the marker (WARN, naming workspace and pid). Without this the cap bounds this **process's** Chromes, not the **host's** — which is the only thing it is for | US-19/AC1+AC2 | crashed-gateway-leaves-no-orphans | `TestPool_ReconcileMarkersAtBoot` | round-2 MAJ-110 |
| **FR-042b** | `cleanStaleSingletons` runs against **each per-key profile dir** before that key's launch, not only `cfg.ProfileDir` (`coordinator.go:1235`). Without it a crash leaves a stale `SingletonLock` per profile and Chrome refuses to relaunch — which makes FR-043 false in the exact case it exists for | US-19/AC3 | idle-close-relaunch | `TestPool_StaleSingletonClearedPerKey` | round-2 MAJ-103 |
| **FR-043** | Reload survival is by **profile on disk**, replacing ADR-043 CRIT-002's context re-adoption | US-17/AC1 | reload-preserves-login | `TestReload_PreservesPIDAndLogin` | D1.1a |
| **FR-043a** | The profile directory has a **deletion** path and exactly one trigger: **workspace deletion**, after `pool.Close(k)` returns. Idle close, roster change, reload, operator close and crash recovery never delete. Directories are created `0700`. A release-note line states the consequence | US-20 | workspace-deletion-deletes-logins | `TestPool_DeleteProfileOnWorkspaceDeletionOnly` | round-2 MAJ-111, ADR D2.11 |
| **FR-044** | **Gate G-1 (human gate — §0.3.1).** `max_browsers`' shipped default is set from a recorded measurement of marginal per-Chrome RSS, not an estimate; the raw measurement and the arithmetic to the default are pasted in the PR body | US-15/AC3 | — | SC-012 (review gate) + `TestConfig_MaxBrowsersDefaultIsNotZeroOrRound` | §0.3 |
| **FR-045** | **Gate G-2 (mechanical gate — §0.3.1).** Before the pool is built, a spike proves `chrome.tabCapture` succeeds for a tab in a **second Chrome's default context** with its own `--user-data-dir`. The test uses `requireBrowserOrFail`, **never** `skipIfNoBrowser`; the gate job sets `OMNIPUS_BROWSER_E2E=1`; the receipt is captured without a pipe and asserted to contain no `--- SKIP` and no `no tests to run` | US-16 | — | `TestSpike_CaptureAgainstSecondChrome` (real Chrome, **fails** without one) | §0.3, round-2 MAJ-106 |
| **FR-046** | An operator-facing **close this workspace's browser** action: `POST /api/v1/workspaces/{id}/browser/close` (204, idempotent, `RequireNotBypass`) plus its SPA control. Closes regardless of tabs and viewers; sends attached viewers a `browser_status` error naming the reason; **keeps the profile**. This is the remedy `errBrowserPoolFull` names | US-18 | pool-refuses-when-all-pinned, close-is-not-deletion | `TestGateway_CloseWorkspaceBrowser`, `TestGateway_CloseWorkspaceBrowser_Idempotent` | round-2 CRIT-103 |
| **FR-047** | The Workspace → Team add-agent surface **states, before confirmation**, that adding an agent grants it every live browser session on that workspace, including on unattended turns. Traced to ADR D2.11's elevation-of-privilege decision, which §1's wording placed in this spec's scope and which no spec had claimed | US-21 | team-add-discloses-grant | `TeamAddAgent.disclosure.test.tsx` (vitest) | round-2 MAJ-114, ADR D2.11 |

**Withdrawn rows are kept, not renumbered,** so that a reader arriving from the round-2 review can see that FR-011/FR-012 were deleted by ruling rather than lost in an edit. They carry no design content.

**Traceability completeness (the round-1 structural PARTIALs closed; round-2's re-checked and its two remaining PARTIALs closed here).** Every US has ≥1 BDD scenario; every AC in §7 is reachable from one; every BDD scenario names its US/AC and FRs inline and has a §10 row. **Eleven FRs deliberately carry no BDD scenario**, and each is a structural, build-time, ordering or measurement requirement rather than an observable behaviour — a Given/When/Then for them would be theatre. Two that were on this list are now off it, because the round-2 revision gave each a check that can fail:

| FR | Why no BDD | How it is verified instead |
|---|---|---|
| FR-002d | A doc comment | Test 9-style doc assertion (`TestLoop_BrowserMgrsCommentIsCurrent`) |
| FR-002e | A build-time migration, not a behaviour | Test 5 (repo-wide, tests included) + §10.1's diff bar |
| FR-016b | Boot-time best-effort warm-up, invisible to any user-facing flow | Test 24 |
| FR-029 | A build gate | `make verify-contracts` |
| FR-030 | A structural import assertion | Test 19 |
| FR-034a | A commit-ordering requirement, not an observable state | Test 9 (stage P) + the §3.3 ordering check, which a reviewer performs |
| FR-035 | A whole-run provenance invariant, not a single interaction | Test 34 |
| FR-036 | Cancellation cascade, covered as a unit-level property | Test 36 |
| FR-037a | Path arithmetic | Test 52 |
| FR-042 | Path construction | Test 27 |
| FR-044 | A measurement (gate G-1) — a **human** gate, §0.3.1 | Recorded in the PR body (SC-012); test 51 catches the round-number shape only |

**FR-019a is no longer on this list.** The previous draft exempted it as "a structural rule over the registry", which was the honest description of a test that could only check membership of a hand-written list. It now has a real BDD scenario (*lease membership follows the control-lock gate*) because the rule became behavioural: exercise each registered tool under a held control lock and under a held write lease, and assert the two answers agree. **FR-045 is also off this list** — §0.3.1 makes it a mechanical gate with a failing check, so it has a test that can fail rather than a note in a PR body.

Everything else in §9 has all four of US, BDD, test and source.

---

## 10. TDD plan (ordered; Unit → Integration → E2E)

| # | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 1 | `TestResolveBrowsingKey_Ladder` | Unit | FR-007 | Table-driven over three rungs. **Write first** |
| 2 | `TestResolveBrowsingKey_NoWorkspaceFailsByName` | Unit | FR-008 | `errors.Is(err, ErrNoBrowsingContext)` **and** key `IsZero()` |
| 3 | `TestResolveBrowsingKey_AmbiguousRefuses` | Unit | FR-033 | Two candidate workspaces, no preference → refusal, both named, WARN on both paths |
| 4 | `TestRegisterTools_NoBoundManagerField` | Unit (structural) | FR-002a | **Predicate stated precisely (round-2 MAJ-105):** reflects over every type in `pkg/tools/browser` **that implements `tools.Tool`**; fails on any `*BrowserManager` field. Not "every exported struct" — `CaptureSession` (`capture_session.go:258`), `LiveViewRegistry` (`live.go:322`), `LiveView` (`live.go:1324`) and `BrowserCoordinator.managers` (`coordinator.go:186`) legitimately hold one and must **not** be "fixed". **Red today** (all eleven tool structs hold `mgr`) |
| 4a | `TestBrowserBuiltinMetadata_ConstructsAllEleven` | Unit (compile-level) | FR-002a | `metadata.go:36-51` builds all eleven tool structs a **second** time with a nil manager. Absent from the previous draft; a compile-level assertion that the metadata list and the registry list have the same names and length |
| 5 | `TestNoResidualDefaultSessionID` | Unit (structural) | FR-002b, FR-002e | **Repository-wide, INCLUDING `_test.go` files:** zero references to `DefaultSessionID`/`defaultSessionID`, with one allowed exception (the migration helper's doc comment). Baseline today: **57 non-test hits (37 executable + 2 declarations + 18 comments) and 364 test-side references across 25 files** (§2.2a). Counting only the non-test side would let a test-only alias keep the constant alive and SC-013 reading zero |
| 6 | `TestControlledResult_UsesResolvedKey` | Unit | FR-002c | Control lock taken under `ws:W`; `controlledResult` must see it. **Red today** (it asks `IsControlled("default")`) |
| 7 | `TestLoop_BrowserManagerForKey_OnePerKey` | Unit | FR-001 | Concurrent callers for one key get one manager; different keys, different managers |
| 8 | `TestListTabsState_ThreeDistinctStates` | Unit | FR-013 | All three states constructed directly; pairwise-distinct payloads; state set is exactly `{no_context, open, empty}` |
| 9 | `TestToolDescriptions_NoFalseSharedClaim` | Unit | FR-015, FR-034 | Asserts the old phrase is gone **and** the new literal is present, verbatim from FR-034 |
| 10 | `TestToolDenial_BrowserSurfaceIsNamed` | Unit | FR-014a | The `browser_*` denial `ModelMessage` names the browser and differs from the generic string |
| 11 | `TestListTabs_DeniedAgentNeverReachesTool` | Unit | FR-014 | Policy-filtered registry; `Execute` not entered |
| 12–17 | `TestWriteLease_*` (OneWriterPerBrowser, LoserGetsDeferredNotError, ReadOnlyToolsUngated, HumanControlTakesPrecedence, ReleasedOnPanicAndCancel, BoundedWait) | Unit | FR-019…FR-024 | Per **§14**; fake clock via §14's named seam |
| 18 | `TestWriteLease_EveryActionToolIsLeased` | Unit (behavioural, registry-enumerated) | FR-019a | **Not a list check.** Enumerates every registered `browser_*` tool and exercises each **twice** — once with a human holding the control lock, once with another agent holding the write lease — asserting the two deferral answers **agree** for every tool. That biconditional is §14 rule 3's rule and it is checkable against shipped code, unlike "does it mutate", which no test can evaluate. Must include `browser_list_tabs` in the never-defers set (round-2 CRIT-104) |
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
| 37 | **`TestSpike_CaptureAgainstSecondChrome`** | **Spike (real Chrome, MUST run)** | FR-045 | **Gate G-2. Run before Stream P starts.** Two Chromes, distinct `--user-data-dir`s, extension loaded in each, capture a tab in the second. **Uses `requireBrowserOrFail`, NOT `skipIfNoBrowser`** — the latter skips in CI without `OMNIPUS_BROWSER_E2E=1` (`browser_e2e_test.go:66-68`) **and** skips when no Chrome probes successfully (the probe ladder at `:69-110` falling through to `resolveTestBinary(t)`'s own `t.Skipf` at `:111`), and both skips report green. Gate job sets `OMNIPUS_BROWSER_E2E=1`; receipt captured without a pipe; log asserted to contain no `--- SKIP` and no `no tests to run` (§0.3.1) |
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
| 50 | `TestConfig_LeaseWaitClampedAgainstPageTimeout` | Unit | FR-023a | `lease_wait=45s` + `page_timeout=30s` → clamped, WARN naming both keys, at load **and** on reload. Asserts the WARN, not only the value — a silent clamp is a config the operator thinks they set |
| 51 | `TestConfig_MaxBrowsersDefaultIsNotZeroOrRound` | Unit | FR-044 | Fails if the shipped default is 0, 5, 10 or 100 — the shapes a guess takes. **Does not prove a measurement happened** (§0.3.1 says so plainly); it catches the common failure, and SC-012 is the real gate |
| 52 | `TestPool_InstallRootIsKeyIndependent` | Unit | FR-037a | `InstallRootForProfileDir` is called with `cfg.ProfileDir` exactly once and never with a per-key path; N keys resolve to one exec path and one install root |
| 53 | `TestPool_ZeroAndNegativeCapAreUnlimited` / `TestPool_TabBudgetStaysGlobalAcrossChromes` | Unit (fake launcher) | FR-038a | 0 and −1 admit five keys; a configured `max_total_tabs=3` is still 3 across two Chromes, not 3 each |
| 54 | `TestPool_RelaunchAfterIdleClose` | Integration | FR-040a | Idle-close, then a tool call: relaunch from the profile, login intact, `LiveKeys()` +1, **same** `*BrowserManager`, no re-registration |
| 55 | `TestReaper_CancelDoesNotStrandPoolEntry` | Unit | FR-040a, CRIT-102 | Drive `ReapIdleSessions` into its all-tabs-idle branch so `se.browserCancel` is cancelled while the pool entry is live; the next `Acquire` must yield a drivable browser |
| 56 | `TestPool_ReconcileMarkersAtBoot` | Unit | FR-042a | 3 markers (2 dead pids, 1 live): all removed, stale locks cleared, live one terminated, INFO + WARN emitted, `LiveKeys()` = 0 |
| 57 | `TestPool_StaleSingletonClearedPerKey` | Unit | FR-042b | A `SingletonLock` planted in `<profileRoot>/ws/W/` is removed before W's launch; one planted in `cfg.ProfileDir` does **not** satisfy the assertion |
| 58 | `TestPool_DeleteProfileOnWorkspaceDeletionOnly` | Integration | FR-043a | Profile removed on workspace deletion (after `Close` returns); **present** after idle close, roster change, reload, operator close and crash recovery — five negative cases, because the positive one alone would pass a "delete always" bug |
| 59 | `TestGateway_CloseWorkspaceBrowser` / `_Idempotent` | Integration | FR-046 | Closes with tabs + viewer attached; viewer gets a `browser_status` error naming the reason; profile survives; second call returns 204 |
| 60 | `TestPool_RefusalRemedyIsEffective` | Integration | FR-039, FR-046 | Cap reached with **every** browser pinned by tabs **and** a viewer; the close named in the refusal frees a slot and the retry succeeds. The existing cap test uses idle browsers — the easy case |
| 61 | `TestPool_PreprovisionAtBootWithNoLiveKeys` | Unit | FR-016c | Resolution/download starts at boot with `len(LiveKeys()) == 0` and no `*BrowserManager` in existence |
| 62 | `TestResolveBrowsingKey_RejectsNonSegmentWorkspaceID` | Unit | FR-037 | `../`, `a/b`, `.`, `..` and an empty id are refused as `ErrNoBrowsingContext` before any path is built |
| 63 | `TestPool_ConcurrentAcquireAtCapBoundary` | Unit (fake launcher, `-race`) | FR-038, FR-039 | Two goroutines `Acquire` **different** keys with exactly one slot left: exactly one wins, the other gets the refusal, `LiveKeys()` never exceeds the cap at any instant |
| 64 | `TeamAddAgent.disclosure.test.tsx` | Unit (vitest) | FR-047 | The disclosure renders before the confirm action, not after it |

### 10.1 Regression requirements (MANDATORY — this change modifies shipped behaviour)

**Must keep passing, with SEMANTICS unmodified — read this bar before reading the list.**

The previous draft said "unmodified" and that was **unsatisfiable, at compile level** (round-2 CRIT-101). FR-002b **deletes** `DefaultSessionID` and `defaultSessionID`; four of the files below reference them **115 times** (`tab_adoption_e2e_test.go` 41, `idle_reaper_test.go` 33, `reaper_edge_test.go` 26, `reaper_lifecycle_test.go` 15), and repo-wide the test surface is **364 references across 25 files** (§2.2a). Those files do not fail an assertion when the constant goes — they fail to build, and take their whole package's test binary with them. "Keep passing unmodified" and "the constant is deleted" could not both hold, and the only way to honour both would have been a test-only alias, which re-creates the exact `"default"` constant SC-013 counts to zero and leaves the reaper suite asserting against a key nothing in production uses.

**The bar, therefore, is behavioural rather than textual.** For every file in this list:

1. The **only** permitted edit is mechanically re-pointing the session-id argument at a browsing key built through `newTestBrowsingKey`. No other line changes.
2. **No assertion is weakened, deleted, inverted, or guarded by `t.Skip`/`t.Short`.** Assertion count per file is unchanged or higher.
3. Test-function count per file is unchanged or higher.
4. The migration lands in the **same commit** as FR-002b (§2.2a item 4) — a commit that deletes the constant and defers the tests leaves `pkg/tools/browser` and `pkg/gateway` unbuildable, at which point every other gate's verdict is meaningless.
5. The reviewer checks the diff against 1–3 directly. A diff that touches anything else in these files is a finding, not a judgement call.

**Files (semantics unmodified):**

- `pkg/tools/browser/tab_adoption_e2e_test.go` — all **nine** ADR-041 tab tests (`:77`–`:569`). Tab-set semantics inside one browser are untouched. **41 references to migrate.**
- `pkg/tools/browser/shared_control_test.go` — **eight** tests (`:35, :55, :74, :92, :109, :138, :157, :186`), not nine as the previous draft claimed. **Correction that matters more than the count:** *this file is not the FR-022 guard.* None of its eight tests calls `controlledResult`; they exercise `LiveView` input dispatch, rate limiting and viewport failure streaks. A green result here proves nothing about the human control lock surviving the re-key.
- **`pkg/tools/browser/tools_control_test.go` — the actual `controlledResult` guard, and absent from the previous draft entirely.** Three tests: `TestExecute_ControlLock_InteractiveToolsDeferWhileControlled` (`:59`), `TestExecute_ControlLock_ReadOnlyToolsAreNotGated` (`:106`), `TestExecute_ControlLock_ReleaseUngatesInteractiveTools` (`:153`). These **must be re-run against the re-keyed lock** and must pass without weakening any assertion. If they need editing, FR-002c is wrong.
- `pkg/tools/browser/idle_reaper_test.go` (**33 refs**), `reaper_edge_test.go` (**26**), `reaper_lifecycle_test.go` (**15**) — FR-025 asserts per-tab reaping is not rewritten. (Whole-Chrome close is *new* behaviour tested separately, in tests 41/54/55, not a change to these.) **Note what these files already cover, which the previous draft's `ReapIdleSessions` claim implied they did not:** the reaper's own browser-context cancellation path (`manager.go:3027-3032`, `:3073-3078`, `:3123-3125`) and its `releaseGlobalTab` → `coord.ReleaseTab` call (`:3118` → `:3358-3365`). FR-040a's new behaviour composes with that path; it does not replace it.
- **The other 21 files in §2.2a's table** — `tabs_test.go` (100 refs), `switch_tab_same_index_recapture_test.go` (26), `focus_emulation_test.go` (24), `switch_tab_activation_test.go` (20), `switch_tab_capture_chain_test.go` (18), `browser_ws_test.go` (15) and the rest — are held to the same bar. They were entirely absent from the previous draft's regression list, which is how a ~364-edit item was budgeted at zero.
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
| `max_browsers=2`, 2 live, request for a 3rd workspace | `errors.Is(err, errBrowserPoolFull)`; text names the cap value + FR-046's close; both live pids unchanged | FR-038, FR-039 |
| W's browser: 0 tabs, 0 viewers, past idle window | process closed; `LiveKeys()` shrinks; profile dir present | FR-040 |
| W1's Chrome killed, W2 live with a viewer | W2 pid/tabs/stream unaffected; W2's manager not reset | FR-041 |
| W1 relaunched after kill | logged in (profile survived) | FR-041, FR-043 |
| reload, 2 agents on W, live login | pid unchanged; `pool.Close` count 0; 1 register/release pair | FR-026a, FR-026b, FR-028 |
| workspace W deleted with a live browser | exactly one `pool.Close(ws:W)` | FR-026 |
| K sub-turns run to completion | pool and manager counts equal baseline | FR-026c |
| attach frame `session_id` from B's chat, agent on A and B | resolves to `ws:B` | FR-016, FR-017 |
| `max_browsers = 0` | unlimited; five keys all admitted | FR-038a |
| `max_browsers = -1` | unlimited; identical to 0 | FR-038a |
| `max_browsers = 2`, `max_total_tabs = 3`, browsers live for W1+W2 | 3 tabs **total** across both, not 3 each | FR-038a |
| cap reached, **every** live browser has ≥1 tab **and** ≥1 attached viewer | refusal; then FR-046's close on one frees a slot and the retry succeeds | FR-039, FR-046 |
| two goroutines `Acquire` two **different** keys with one slot left | exactly one wins; the other refused; `LiveKeys()` never exceeds the cap | FR-038, FR-039 |
| W idle-closed, then a tool call arrives | Chrome relaunches from W's profile, login intact, `LiveKeys()` +1, **same** manager | FR-040a |
| `ReapIdleSessions` cancels `se.browserCancel` while `ws:W` is live in the pool | next `Acquire(ws:W)` yields a drivable browser; no live-but-dead key | FR-040a, CRIT-102 |
| stale `SingletonLock` in `<profileRoot>/ws/W/` after a crash | W's next launch succeeds; the file is removed first | FR-042b |
| boot with 3 stale `ws-*.pid` markers (2 dead pids, 1 live Chrome) | 0 orphan Chromes, 0 stale markers, 0 stale locks; INFO + WARN | FR-042a |
| workspace W deleted with a live browser | `pool.Close(ws:W)` once, **then** `<profileRoot>/ws/W/` removed | FR-026, FR-043a |
| W idle-closed / roster-emptied / reloaded / operator-closed / crash-recovered | profile directory **present** in all five cases | FR-043a |
| `lease_wait = 45s`, `page_timeout = 30s` | clamped at load and reload; WARN names both keys; contended call still returns `deferred`, not a CDP error | FR-023a |
| boot, fresh install, **zero** live keys | managed-Chromium resolution/download has started | FR-016c |
| workspace id `../evil`, `a/b`, `.`, `..`, `""` | `ErrNoBrowsingContext`; no path constructed | FR-037, MIN-106 |
| every registered `browser_*` tool, under a held control lock vs a held write lease | the two deferral answers agree for all of them; `browser_list_tabs` defers under neither | FR-019a |

---

## 11. Functional requirements & success criteria

- **FR-001 … FR-047** as enumerated in §9 (FR-011 and FR-012 withdrawn as tombstones). All MUST. **Counts: 69 rows in §9, 2 of them withdrawn tombstones (FR-011, FR-012), so 67 live FRs** — 55 carried forward from the previous draft plus **12 added by this round-2 revision**: FR-002e, FR-016c, FR-023a, FR-034a, FR-037a, FR-038a, FR-040a, FR-042a, FR-042b, FR-043a, FR-046, FR-047. Of the twelve, **two are new scope** (FR-046 the operator close, FR-047 the team-membership disclosure) and **ten close gaps inside scope the previous draft had already claimed**.

Every criterion below states **what would make it fail.** Round-1 found four gates that could not fail for the defect they named; each was rewritten and its failure mode spelled out. **Round-2 found three more** — test 37 behind a skip, SC-012 with no executable form, and SC-015 with no mechanical gate — and rewrote or resolved each: SC-012a is mechanical with four stated conditions, SC-012 is declared a human gate with a named owner and a named artefact (§0.3.1), and SC-015 is satisfied by ADR-072 D1.1a's blanket attribution. **A criterion with no failing check is now stated as a human gate rather than left to look like a test.**

- **SC-001 (headline, the reported defect).** Browse as Mia in workspace W; switch the chat to Jim; Jim's `browser_list_tabs` returns the tab. Measured by test 38, `TestHandover_ThroughRealRegistrationPath`, which goes through `registerSharedTools` — **not** a hand-built manager. *Fails if:* the two agents resolve different managers. **This test is red today**; if it is green before the change, it is not exercising the real path.
- **SC-002 (isolation exists and is by profile).** `TestBrowsingContext_CrossWorkspaceIsolation` passes against real Chrome, asserting a missing cookie **and** distinct pids **and** distinct `--user-data-dir` paths, under the **shipped default configuration** — no flag flip, no env var. *Fails if:* the cookie is present, the pids match, or the test needs a non-default config to pass. **The previous SC-002 could only pass with `capture_shared_context=false`, i.e. it proved a property of a configuration nobody ships.** FR-031 removes the flag so this criterion has no configuration to hide behind.
- **SC-003 (no silent merge, behavioural not just structural).** Zero `pool.Acquire` calls in a full run carried a key that did not come from a `ResolveBrowsingKey` return in the same turn (test 34). *Fails if:* any acquire's key is untraceable. The old SC-003 asserted only that `BrowsingKey`'s field was unexported — which constrains key *construction*, never *use*, and would not have caught a parent's key being passed inside a sub-turn.
- **SC-004 (delegated work shares, and leaks nothing).** `TestSubTurn_UsesWorkspaceBrowser` asserts the sub-turn is logged in on `ws:W`, and `TestSubTurns_NoPoolGrowth` asserts K sub-turns return the pool and manager counts to baseline. *Fails if:* a sub-turn gets its own key, or the counts grow. **This inverts the previous SC-004** (which asserted a distinct context for the sub-turn) because the ruling inverted the requirement — recorded so the reversal is visible rather than looking like a deleted test.
- **SC-005 (concurrency is deterministic).** Eight concurrent action tools on one workspace browser, repeated 50× under `-race`: zero errors, zero deadlocks, exactly one executing writer at any instant. *Fails if:* any interleaving, error, or hang.
- **SC-006 (three states).** A table-driven test enumerates all three `ListTabsState` values and asserts pairwise-distinct model-visible payloads; a fourth value is a compile-time impossibility. *Fails if:* any two payloads are equal, or a new value is added without updating the test.
- **SC-007 (contract intact, including its meaning).** Three conditions, all required. (1) `make verify-contracts` exits 0. (2) The `contracts/` diff contains no `properties:`, `required:`, `enum:` or `type:` change. (3) **`TestGateway_SessionIDIsBinding` passes** — an attach frame whose `session_id` belongs to workspace B's chat resolves to B for an agent on both A and B. *Fails if:* the resolution ignores `session_id`. **Condition (3) is the fix for the round-2 finding that this gate could not fail:** conditions (1) and (2) are shape checks, and the change FR-016 makes is a *semantic reversal* of a documented guarantee (`BrowserAttachFrame.yaml`: the server binds *"regardless of the value sent here"*). A shape check passes a reversal cleanly. Additionally, the two replacement description strings must be reviewed against FR-016's verbatim text, and `BrowserInspectRequest` must be confirmed **not** to have gained chat-session semantics (US-10/AC3).
- **SC-008 (nothing green by accident).** Every rewritten test in §10.1 is confirmed to **fail** against the pre-change code and **pass** after. **Extended to the four tests round-1 identified as unfalsifiable**, each of which must be red today: test 4 (`TestRegisterTools_NoBoundManagerField`), test 6 (`TestControlledResult_UsesResolvedKey`), test 32 (`TestReload_PruneUsesBrowsingKeys`), test 38 (`TestHandover_ThroughRealRegistrationPath`). A test that passes both ways is not evidence. **Round-2 OBS-101 is accepted and this criterion is no longer phrased as an intention:** each of the four is *expressible* against current code (test 4 structurally — all eleven structs hold `mgr`; test 6 by taking the control lock under a non-`"default"` id; test 32 by seeding `al.browserMgrs` with a `"ws:W"`-shaped key and running the prune; test 38 through `registerSharedTools`), so "must be confirmed to fail" costs four commands. *Fails if:* the four `exit=` receipts are not pasted into the PR **before** the implementing commits — captured without a pipe, per §10.1's receipt discipline. A red-then-green claim made after the fix has landed is not reproducible and does not satisfy this criterion.
- **SC-009 (the control lock is provably alive).** `tools_control_test.go`'s three tests pass against the **re-keyed** lock, with no assertion weakened. *Fails if:* any assertion is relaxed to accommodate the re-key. **`shared_control_test.go` is explicitly not this criterion's guard** — it never calls `controlledResult`.
- **SC-010 (the pool is bounded and honest).** With `max_browsers = N`: `len(pool.LiveKeys()) <= N` at every instant of a run that requests N+3 workspaces; the three refusals satisfy `errors.Is(err, errBrowserPoolFull)`; and no live browser's pid changed. *Fails if:* the pool ever exceeds N, or a refusal is served by closing something.
- **SC-011 (a crash is contained).** Killing one workspace's Chrome leaves every other workspace's pid, tab set, viewer stream and cookies intact, and does not reset their managers. *Fails if:* any other key is affected — which is today's behaviour (`watchForCrash` resets every connector manager).
- **SC-016 (the pool leaves nothing on the host).** After a `kill -9` of the gateway with three workspace browsers live, the next boot leaves **zero** Chrome processes from the previous run and **zero** `$OMNIPUS_HOME/browser/ws-*.pid` markers, and each of the three workspaces' next browser call launches successfully from its own profile. *Fails if:* any orphan Chrome survives (it consumes the host memory the cap exists to bound while being invisible to `LiveKeys()`), **or** any workspace's relaunch fails on a stale `SingletonLock` — the second is the one that silently falsifies FR-043's "the login survives" promise.
- **SC-017 (a departed client's data departs).** After deleting a workspace that had a live browser with a session cookie, `<profileRoot>/ws/<id>/` does not exist. *Fails if:* it does — and separately, if the directory is removed on **any** of idle close, roster change, reload, operator close or crash recovery, because that logs the operator out in five situations where the profile is the whole point.
- **SC-018 (a full pool is escapable).** With the cap reached and every live browser pinned by both an open tab and an attached viewer, an operator following **only** the actions the refusal message names reaches a working browser for the new workspace. *Fails if:* the message names an action that does not free a slot. **This is the criterion the previous pool-full text failed:** it said "close a browser panel", which detaches a viewer and nothing more, while idle close requires zero tabs **and** zero viewers.
- **SC-012 (G-1: the memory measurement — a HUMAN gate, and this criterion says so).** The implementing PR body contains: the raw per-Chrome RSS measurement for N = 1, 2, 3, 4 Chromes (browser process only, one blank tab each), the tool and command that produced it, the host's total RAM, and the arithmetic from those numbers to `max_browsers`' shipped default. *Fails if:* the default is a round number with no measurement behind it, **or** the arithmetic from the measurement to the default is absent. **Owner: the implementing PR's human reviewer**, who must not approve without seeing the arithmetic. §0.3.1 explains why no mechanical form of this gate is honest — a test asserting "a measurement file exists and is non-empty" passes on a fabricated file, which makes it a second place to write the guess rather than a check on it. Test 51 is the partial mechanical half and catches only the round-number shape.
- **SC-012a (G-2: the capture spike — a MECHANICAL gate).** `TestSpike_CaptureAgainstSecondChrome` **ran and passed** before Stream P's first commit. All four conditions required: (1) the test uses `requireBrowserOrFail`, and `grep -c skipIfNoBrowser` in its file returns 0 for that test; (2) the gate invocation sets `OMNIPUS_BROWSER_E2E=1`; (3) the receipt was captured as `cmd > log 2>&1; echo "exit=$?"` — never through a pipe — and reads `exit=0`; (4) the log contains neither `--- SKIP` nor `no tests to run`. *Fails if:* any of the four is missing. **A skipped result is a FAILED gate, not a passed one** — this is the single load-bearing assumption of D1.1a, and the equivalent claim for CDP contexts proved false against real Chrome 150 (`coordinator.go:330-348`). The previous draft's version of this gate could not fail: `skipIfNoBrowser` skips in CI without the env var (`browser_e2e_test.go:66-68`) **and** skips when no Chrome probes successfully (`:69-111`), and no gate set the variable.
- **SC-013 (no residual constant, tests included).** Repo-wide references to `DefaultSessionID`/`defaultSessionID` are **zero**, in production **and** test code — down from 57 non-test hits (37 executable + 2 declarations + 18 comments) and **364 test-side references across 25 files**. *Fails if:* any survives, including a test-only alias. A test-only alias would leave this criterion reading zero while the constant is alive, the reaper suite still asserting against `"default"`, and the measurement meaningless — which is why the count is repo-wide rather than non-test (§2.2a).
- **SC-014 (gates).** `gofmt -l . | wc -l` = 0; `golangci-lint run --build-tags=goolm,stdjson` exit 0; CI `go test -tags goolm,stdjson -count=1 ./...` exit 0; `govulncheck` 0 vulnerabilities; `npm run typecheck` exit 0; `npx vitest run` exit 0. **Not sufficient on its own:** SC-012 and SC-012a are separate and neither is satisfied by a green CI run. Note also that `golangci-lint` caps findings at 3 per message by default — read `docs/internal/false-green-patterns.md` before reporting a clean lint.
- **SC-015 (attribution) — SATISFIED, recorded rather than deleted.** §12 A10 asked for the operator of record for the 2026-08-31 rulings. **ADR-072 D1.1a's closing paragraph now settles it for the whole document:** *"Decider for every ruling in this ADR: Daniel Piatkowski (operator), 2026-08-31. Recorded once here so the individual 'operator ruling' citations in D1.0, D1.1a, D1.2, D1.4, D2.9 and D2.11 have a named authority — a spec cannot resolve its own provenance."* That covers the isolation axis, the no-fallback rule, the browser seed and the lease ownership. The criterion is kept as a satisfied row rather than removed so a reader does not re-raise it. *Would fail if:* a future D1 ruling is added reading "operator ruling, 2026-08-31" without falling under that blanket attribution.

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
| **A10** | **Operator of record for the 2026-08-31 rulings.** | **RESOLVED, 2026-08-31.** ADR-072 D1.1a's closing paragraph attributes **every** ruling in the ADR to Daniel Piatkowski (operator), naming D1.0, D1.1a, D1.2, D1.4, D2.9 and D2.11 explicitly. SC-015 is kept as a satisfied row rather than deleted, so this is not re-raised. |
| **A11** | **Two gateway processes on one `$OMNIPUS_HOME`.** The write lease is in-process only (FR-030, correctly — `fileutil.WithFlock` is a documented no-op on Windows). The pool adds a per-key on-disk launch lock, which inherits the same Windows no-op. | **DECIDED: out of scope, stated rather than silent.** Two gateways on one home are already unprotected for all six file stores on Windows (ADR-054 §5.1) and the pool neither worsens nor fixes that. On POSIX the per-key `flock` gives the same single-launch guarantee the current singleton lock gives. Filed as follow-up with ADR-054's `LockFileEx` work, not solved here. |
| **A12** | **Does a workspace browser outlive the last agent that can use it?** FR-026 said "roster change" without a predicate. | **DECIDED (FR-026a):** a workspace key is live while the workspace exists **and** has ≥1 browser-policy-allowed agent on its CoreTeam. Losing the last such agent closes the browser (the profile survives). This is also the prune's liveness predicate, so the two can never disagree. |
| **A13** | **What happens to a running sub-turn's browser work when the parent is cancelled?** | **DECIDED (FR-036):** ADR-057's inherited `routingSessionID` makes chat-wide Stop cascade to sub-turns, which cancels the in-flight tool call and releases the lease (FR-024). **No browser is closed** — the browser belongs to the workspace, not the turn, so a cancel must not log the operator out. |
| **A14** | **Is `ResolveBrowsingKey` evaluated once per turn or once per tool call?** | **DECIDED: once per tool call**, inside `ManagerResolver.ManagerFor` (FR-002a). Per-call is required anyway because the manager must be resolved per `Execute`; and since there is now exactly one key shape and no viewer-dependent branch, per-call resolution is **deterministic within a turn** — it cannot change under the caller the way the withdrawn attendance check could. No caching layer is specified; if profiling later demands one, it caches per turn, never across turns. |
| **A15** | **`max_browsers`' default value.** | **UNRESOLVED BY DESIGN — it is a measurement, not a decision.** FR-044/G-1 must produce the marginal per-Chrome RSS figure first. Recorded here so nobody ships a plausible-looking constant. The *shape* is decided: a positive integer, config key `tools.browser.max_browsers`, with the same reload-without-restart behaviour `max_total_tabs` already has (`coordinator.go::SetMaxTotalTabs`, `::ApplyRuntimeConfig`). |
| **A16** | **Does `chrome.tabCapture` actually work against a second Chrome's default context?** ADR D1.1a asserts each Chrome carries its own extension, so yes. | **UNRESOLVED — GATE G-2 (FR-045, SC-012a).** Flagged rather than assumed because this is the same *class* of claim that proved false for CDP contexts (`coordinator.go:330-348`, verified against real Chrome 150), and that falsification cost an entire design. Prove it with a spike **before** Stream P is built, under §0.3.1's four conditions — a skipped test is a failed gate. |
| **A17** | **`browser_handle_dialog` is exempt from the write lease, and §14 rule 3 claimed "membership is a rule, not a list" while listing a MUTATING tool as exempt.** The rule and its own exemption contradicted each other, so test 18 could only ever check a hand-written list — the list-driven test MAJ-008 was raised to eliminate — and that list had already been widened 3→5 the same day under pressure from the sibling spec, with no criterion distinguishing a legitimate widening from a convenient one. | **DECIDED — the rule is restated as a biconditional that actually holds, and it is not "does it mutate".** A `browser_*` tool takes the write lease **iff** it is gated by the ADR-038 D6 human-control lock (`controlledResult`). Over the eleven tools shipped today that biconditional holds **exactly**, with no exceptions: `controlledResult` is called by `browser_navigate` (`tools.go:119`), `browser_click` (`:232`), `browser_type` (`:429`), `browser_evaluate` (`:879`), `browser_switch_tab` (`tabs.go:113`), `browser_close_tab` (`:171`) and `browser_open_tab` (`:239`) — and by none of `browser_screenshot`, `browser_get_text`, `browser_wait` or `browser_list_tabs`, whose file says so outright (`tabs.go:20`). One classification, two consumers, no second list. **`browser_snapshot` falls out correctly** as read-only and needs no exception. **`browser_handle_dialog` is exempt from BOTH gates, not one** — that is what makes it coherent rather than arbitrary: a JS modal blocks the renderer, so the panel's own `Input.dispatch*` injection is blocked too and a human holding the wheel is **equally** unable to clear it; only `Page.handleJavaScriptDialog` can. Exempting it from the lease but not the control lock would leave the tab wedged for the human as well. **This places one obligation on the D2 spec:** it must register `browser_handle_dialog` ungated by `controlledResult`. If D2 declines, the biconditional acquires its first exception and §14 rule 3 reverts to a list — flagged for the operator rather than assumed. **Unspecified interaction, now specified:** after `browser_handle_dialog` clears a modal, the still-blocked leaseholder resumes and acts on a page that changed underneath it. Its result is **not** undefined by fiat — the tool that resumes must re-verify its own precondition before completing (a `browser_click` re-resolves its selector; a `browser_navigate` re-checks the current URL), and FR-024's `defer release()` means the lease is released either way. |
| **A18** | **A `tools.browser.profile_dir` change on reload, under N per-key directories derived from it.** | **DECIDED — preserve the shipped behaviour per key, rather than invent a relocation.** Today `ApplyRuntimeConfig` logs a WARN and does **not** apply a `profile_dir` change to a running Chrome (`coordinator.go:681-687`: *"applies after gateway restart"*). The pool keeps exactly that: the change is not applied to any live Chrome, the WARN now names how many keys are affected, and every key re-derives its path from the new `profileRoot` at the **next restart** — at which point every workspace is logged out, because the profiles it is pointed at are new empty directories. That consequence is stated in the WARN text and in the config key's doc comment. Relocating N profiles live was rejected: it means copying hundreds of megabytes per workspace while Chrome holds files open in each. |
| **A19** | **Is FR-033's refusal actually cheaper than stamping the workspace onto the turn?** The round-2 review argued the scheduled path already stamps it (`loop.go:6933-6957`) and that FR-033 therefore pays a near-certain cost — a multi-workspace agent losing its browser on every heartbeat — for a case the platform already solves. | **DECIDED — keep FR-033, and the premise the objection rested on is corrected (§16 MAJ-113).** The stamping path is real and is now cited in §6 and in US-6/AC0, but it **already covers heartbeats**: jobs are named `heartbeat:<workspaceID>:<agentID>` (`heartbeat_schedule.go:30-33`) and that workspace is parsed back and stamped before the run (`schedules.go:654`, `:513`, `:527-576`), so a heartbeat resolves at **rung 1** and never reaches FR-033. Adding Ray to a second workspace creates a *second, distinct* heartbeat job with its own workspace — it does not make his existing heartbeats ambiguous. The residual is a **plain, operator-created** schedule, which resolves to `""` because ADR-065 FR-8 removed the channel source (`schedules.go:632-639`); for a multi-workspace agent that turn is refused. **The alternative — a per-agent "browsing home workspace" — is declined:** it is a new agent-level field for a case the platform is already migrating away from (ADR-037 removed the global delegation graph for exactly the reason that "the owner's workspace" is ambiguous for a multi-workspace agent, and `resolveScheduleWorkspaceID`'s own doc rejects `CronJob.AgentID` as a source on the same grounds). Adding one back for browsing would re-introduce the global-agent-attribute shape this project deliberately removed. |
| **A20** | **What does boot reconciliation do with a stale ownership marker whose pid is still alive?** | **DECIDED: terminate it, and the trade-off is recorded rather than buried.** A live pid named by a marker under **this** `$OMNIPUS_HOME` is either an orphan of a crashed gateway on this home (the common case) or a second gateway on the same home (already unsupported — §12 A11 — and already refused outright by the shipped single-Chrome path, `coordinator.go:1458-1467`). Nothing can distinguish them, and leaving it alive means unbounded host memory outside the cap, which defeats FR-038's only purpose. **Pid reuse is a real hazard** — a dead gateway's Chrome pid can be reused by an unrelated process — and the shipped code has it too (`pidAlive(pid)` plus an owner string read from the *marker file*, not from the process). Mitigation, stated honestly by platform: on Linux, confirm `/proc/<pid>/exe` resolves to the resolved Chrome binary before terminating. **On macOS and elsewhere there is no pure-Go equivalent** (Hard Constraint #2 forbids shelling out here), so on those platforms the marker is **removed without terminating** and a WARN names the pid so an operator can act — a smaller guarantee, stated rather than implied. |
| **A21** | **`max_browsers`' 0/negative semantics, and its relationship to `max_total_tabs`.** | **DECIDED (FR-038a): same shape, different default, and the difference is argued.** `<= 0` means **unlimited**, matching `max_total_tabs` (`coordinator.go:785-788`, guarded by `TestCoordinator_UnlimitedDefault_AllowsPastOldCap`) — an operator who knows one key will assume the other, and surprising them is worse than the extra branch. The **defaults** differ deliberately: `max_total_tabs` ships unlimited because an unbounded tab count is bounded in practice by the per-tab idle reaper on a 5-minute TTL (its own doc comment makes this argument), whereas an unbounded **browser** count is bounded by nothing — a Chrome browser process costs its baseline RSS at zero tabs, and the idle-close TTL is 15 minutes rather than 5. So `max_browsers` ships a positive measured integer (FR-044). **The tab budget stays global** across all N Chromes rather than becoming per-Chrome: it is already a coordinator-level counter, and making it per-Chrome would silently multiply an operator's configured ceiling by N. Consequence, stated: one workspace can exhaust the global tab budget and starve another. That is today's behaviour across agents, it is unchanged by the pool, and it is not made worse by it. |
| **A22** | **The whole-Chrome idle window's value.** | **DECIDED: `tools.browser.idle_close_ttl`, default 15 minutes — a reasoned default, not a measurement, and the spec says which.** Derivation: it is 3× the per-tab `tools.browser.idle_ttl` default of 5m (`manager.go:134`), so a Chrome closes only after its last tab has been gone for a further two tab-TTLs. The asymmetry is deliberate — per-tab reaping already reclaims the renderer processes, which are the dominant cost (74–268 MB RSS each, measured, `config.go:3665-3667`); closing the browser process reclaims a smaller fixed cost and pays a relaunch (~1–3s plus a cold page cache) on the next use, so it should be less eager than tab reaping, not equally eager. **The sweep interval invariant applies:** the existing 1-minute ticker (`gateway.go:5322`) must stay well under this TTL, or the TTL becomes a floor rather than the lifetime — the reason that comment gives for the interval being 1 minute against a 5-minute tab TTL holds a fortiori at 15. **This is not FR-044's kind of number** and §5's "no default derived from an estimate" non-behaviour does not apply to it: that rule is about the cap, whose wrong value costs host memory or a false refusal; this one's wrong value costs a relaunch. |

**Corrections folded in above, listed so a reviewer can check them:** the ADR's `pkg/agent/loop.go:185` is `:279`; "six tool descriptions" is 5 model-visible strings + 2 Go comments + 1 unrelated SPA comment; `pkg/tools/base.go:241-251` is `:243-252`; `pkg/tools/resolvepath.go:695-709` is prose whose call is at `:713`; the viewer counters are cited by symbol (`ViewerAttached`/`ViewerDetached`) rather than by the two disagreeing line numbers the previous draft gave; `shared_control_test.go` has **eight** tests, not nine; the registered tool name is **`serve_web`** (`pkg/tools/web_serve.go:46`), not `web_serve`; `BrowserManagerForAgent` takes **one** argument today, not two.

**Round-2 corrections, added:** `ReapIdleSessions` **does** cancel browser contexts and **does** reach the coordinator — the previous draft's *verified* claim was false in both places it appeared (§2.1, §15 CRIT-006, and ADR §8's corrections log); the `DefaultSessionID` grep returns **57** lines (37 usages + 2 declarations + **18** comments, not 12), and `pkg/config/config.go:3892` was missing from the file inventory; the test-side surface is **364 references across 25 files**, previously unbudgeted; `sandbox_apply.go`'s removal note is at **`:412-417`**, not `:405-417`; the exempt tool set is **six**, not five/three/"five D2 tools"/"four D2 tools" — four different figures appeared in the previous draft; the action-tool timeout `leaseWaitTimeout` must stay under is `BrowserConfig.PageTimeout`, config key **`tools.browser.page_timeout`** (not `page_timeout_sec`, which is the Go field's suffix); heartbeat turns **do** carry a workspace, so FR-033's stated cost was overstated (§16 MAJ-113).

**One correction this spec does NOT own, noted so it is not lost:** ADR-072 **D1.3's key table still reads** *"Transcript session · `tools.ToolTranscriptSessionID(ctx)` · … · Used for: Unattended delegated work — D1.2"* — residue of the design the superseding D1.2 ruling deleted. §2.1 correctly marks that key **not used**, so the ADR and its spec now disagree and the ADR is the source of record. Amending it is the ADR's owner's edit, not this spec's (round-2 MIN-108).

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
11. **(edge)** An agent is added to a second workspace mid-session. Its next turn in the original chat still resolves to the original workspace (the chat's `workspace_id` wins) — no silent browser swap. Its next *heartbeat* on **each** workspace resolves to **that** workspace's browser, because a heartbeat job carries its workspace in its own name (§6, US-6/AC0) — **not** refused as ambiguous. *(This holdout previously asserted the refusal, which was the wrong expectation; round-2 MAJ-113.)* A **plain, operator-created schedule** for that same agent, which carries no workspace at all, **is** refused as ambiguous (FR-033) — that is the case worth watching for.
12. **(edge, the pool)** Operator opens browsers in `max_browsers` workspaces, **keeps a tab and a panel open in every one of them**, then starts work in one more. They get a clear refusal naming the cap — and, critically, **nothing they had open closed**. They then follow the refusal's own instructions: close one workspace's browser from the UI, retry, and it works. *(The pinned-in-every-workspace setup is the point: with idle browsers the refusal resolves itself and the test proves nothing.)*
13. **(edge, the pool)** The same operator instead raises `max_browsers` in Settings and saves. The new browser opens **without a restart**, and nothing that was open was disturbed.
14. **(error, the pool)** One workspace's Chrome is killed from outside (Activity Monitor / `kill`). That workspace's panel shows an error and recovers on next use with its login intact; every other workspace keeps streaming without interruption.
15. **(edge, memory)** Leave the gateway running with several workspaces browsed and then idle overnight. Chrome process count returns to zero, RSS returns to baseline, and the next morning's first browser call in any of those workspaces is still logged in.
16. **(error, crash recovery)** Operator kills the gateway with `kill -9` while three workspaces have live browsers, then starts it again. Activity Monitor shows **no** Chrome left from the previous run; each of the three workspaces' first browser call afterwards works and is still logged in — no "profile in use" failure.
17. **(edge, deletion)** Operator deletes a client's workspace, then inspects `~/.omnipus/browser/profiles/ws/`. That workspace's directory is gone. They can answer the client's "is my data deleted?" with yes.
18. **(edge, disclosure)** Operator adds an agent to a workspace that is signed into a real site. Before confirming, the UI tells them the agent will be able to act as that signed-in user, including on turns nobody is watching. They can decide with that in front of them.

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
//
// THE TIMEOUT IS NAMED, AND THE RELATIONSHIP IS ENFORCED RATHER THAN COMMENTED
// (round-2 MAJ-112). It is BrowserConfig.PageTimeout (manager.go:35, default
// 30s at :123), operator-settable as tools.browser.page_timeout
// (config.go:3632's PageTimeoutSec, applied at loop.go:2311-2312). BOTH values
// are operator-configurable and the previous draft validated neither, so an
// operator could set lease_wait=45s against page_timeout=30s and turn every
// contended call into a CDP timeout ERROR where FR-020 requires a non-error
// deferral. TestWriteLease_BoundedWait only ever asserted the defaults.
//
// FR-023a: at config load AND on reload, leaseWaitTimeout is CLAMPED to
// min(configured, PageTimeout/2) with a WARN naming both keys and both values.
// Clamping rather than rejecting, deliberately: aborting boot over a browser
// tuning key is disproportionate, and a clamp preserves the contract the
// operator actually cares about (a deferral, not an error). The WARN is part
// of the requirement — a silent clamp leaves the operator believing a setting
// took effect that did not. Asserted by test 50, at load and at reload.
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
// adopts the D2 spec's pre-built-*ToolResult* shape so D2's FOUR leased new
// action tools (select_option, press_key, hover, upload_file — its other two,
// snapshot and handle_dialog, are exempt; see §14 rule 3's normative counts)
// do not hand-roll the deferral body, while keeping D1's stronger primitive
// underneath (cancellable, bounded, names the holder).
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
3. **Membership is a rule — and this is the rule that actually holds** (FR-019a).

   > **THE NORMATIVE COUNTS, stated once and referenced from everywhere else: the exempt set is SIX. Eleven tools are leased — seven shipped today plus four of D2's six new ones. D2 adds SIX tools, of which four are leased and two are exempt.**
   >
   > *(The previous draft stated these four different ways in one document — "a closed, named set of **five**" here, "a closed set of **three**" in §15's MAJ-008 disposition, "D2's **five** new action tools" in §14.1's `leaseWrite` comment, and "D2's **four** remaining new action tools" in this rule's own closing sentence. Round-2 MAJ-101. Every one of those figures is now derived from the table below rather than restated.)*

   **The rule:** a `browser_*` tool takes the write lease **if and only if** it is gated by the ADR-038 D6 human-control lock — that is, if and only if its `Execute` calls `controlledResult`.

   **Why this rule and not "does it mutate".** The previous draft's rule was *"every tool that mutates page or tab state acquires the lease; the exemption list is exactly the five"* — and `browser_handle_dialog` **mutates page state** (it accepts or dismisses a modal) while being on the exemption list. The rule contradicted its own exemption, so no test could classify by it; test 18 could only check membership of a hand-written list living in this same document, which is the list-driven test MAJ-008 was raised to eliminate. That list had already been widened 3→5 the same day under pressure from the sibling D2 spec, with no stated criterion separating a legitimate widening from a convenient one (round-2 MAJ-102).

   The control-lock gate is a **different** and better classifier: it already exists in shipped code, it was decided in ADR-038 D6 on the same question ("may an agent do this while a human is driving?"), and it partitions the eleven shipped tools **exactly** the way the lease needs to. One classification, two consumers, no second list to drift.

   | Tool | `controlledResult`? | Lease? | Evidence / why |
   |---|---|---|---|
   | `browser_navigate` | yes (`tools.go:119`) | **leased** | mutates the page |
   | `browser_click` | yes (`tools.go:232`) | **leased** | mutates the page |
   | `browser_type` | yes (`tools.go:429`) | **leased** | mutates the page |
   | `browser_evaluate` | yes (`tools.go:879`) | **leased** | arbitrary JS |
   | `browser_switch_tab` | yes (`tabs.go:113`) | **leased** | mutates tab state |
   | `browser_close_tab` | yes (`tabs.go:171`) | **leased** | mutates tab state |
   | `browser_open_tab` | yes (`tabs.go:239`) | **leased** | mutates tab state |
   | `browser_screenshot` | no | **exempt** | read-only; ungated today |
   | `browser_get_text` | no | **exempt** | read-only; ungated today |
   | `browser_wait` | no | **exempt** | read-only; ungated today |
   | **`browser_list_tabs`** | **no** (`tabs.go:20`: *"Read-only — NOT gated by controlledResult"*) | **exempt** | **The omission that would have broken the headline demo.** It is registered (`register.go:76`), read-only, and was in neither of the previous draft's categories — so under the old AC5 it would have taken the **write** lease, making Jim's `browser_list_tabs` (behavioural contract 1, US-1/AC1, the headline BDD scenario) return `{"deferred": true}` behind another agent's long `browser_navigate`. Round-2 CRIT-104 |
   | `browser_snapshot` (D2 FR-018) | no (D2 must keep it so) | **exempt** | read-only — falls out of the rule, needs no exception |
   | `browser_handle_dialog` (D2 FR-035) | **no — D2 must register it ungated** | **exempt** | **Recovery, exempt from BOTH gates and that is what makes it coherent.** A JS modal blocks the renderer, so the panel's own `Input.dispatch*` injection is blocked too: a human holding the wheel is **equally** unable to clear it, and only `Page.handleJavaScriptDialog` can. Exempting it from the lease but not the control lock would leave the tab wedged for the human as well. See §12 A17 |
   | `browser_select_option`, `browser_press_key`, `browser_hover`, `browser_upload_file` (D2) | yes (D2 must gate them) | **leased** | mutate the page; automatically in scope by the rule, with no edit here |

   **What test 18 checks, and why it can now fail for the right reason.** It enumerates the **registry** and exercises each registered `browser_*` tool twice — once with a human holding the control lock, once with another agent holding the write lease — asserting the two deferral answers **agree** for every tool. That is the biconditional, checked behaviourally against shipped code. It is no longer possible to make it green by editing a list in this document; making it green requires changing a tool's actual gating.

   **The one cross-spec obligation.** D2 must register `browser_handle_dialog` ungated by `controlledResult`, and must gate its four action tools. If D2 declines the first, the biconditional acquires its first exception, this table becomes a list again, and that is an operator decision rather than a spec's — flagged in §12 A17 rather than assumed.
4. **`release()` is idempotent and MUST run via `defer`** in every leased tool, so a panic, a CDP timeout or a cancelled context cannot wedge the browser (FR-024).
5. **Lock order** is `writeLease → pool.mu → m.mu`, never reversed; `m.mu` is never held across `acquireWrite` or any CDP call.
6. **In-process only** (FR-030). The lease deliberately does not use `fileutil.WithFlock`, which is a documented no-op on Windows (`pkg/fileutil/flock_windows.go`) and would give a false cross-process guarantee. Two gateways on one home are out of scope — §12 A11.
7. **No fairness guarantee** under sustained contention (§12 A8). The bound is what is promised, and FR-023 tests it.

---

## 15. Round-1 review disposition (all 29 findings of `…-spec-review.md`)

| ID | Disposition | Where / evidence |
|---|---|---|
| **CRIT-001** — isolation is off by default; enabling it breaks the live panel | **Superseded by ruling, then fixed.** The operator rejected the trade; ADR D1.1a replaces CDP-context isolation with **Chrome-process + profile isolation**, which delivers both. The finding's diagnosis was correct and is preserved verbatim in §0.1 with its evidence | §0.1, FR-031, FR-037, FR-045; gate G-2 |
| **CRIT-002** — tools hold a manager bound at registration | **Fixed.** `register.go:41-84` and the 11 `mgr:`-bearing structs added to §2.1; `ManagerResolver` added to the interface contract; FR-002a; a **structural** test (no `*BrowserManager` field) and an **end-to-end** test through `registerSharedTools` that is red today | §2.1, §3.1, FR-002a, tests 4 + 38, SC-001 |
| **CRIT-003** — reload prune keys off agent ids | **Fixed.** `loop.go:2849-2871` verified; FR-026a makes the predicate the live-key set with an explicit liveness definition (§12 A12); FR-026b makes registration idempotent per key; the reload BDD now specifies **two agents** and asserts `pool.Close` count **zero** | FR-026a, FR-026b, tests 32/33/42, US-17 |
| **CRIT-004** — the lease is double-specified with incompatible APIs | **Fixed.** §14 is the single normative definition, per operator ruling. It keeps D1's stronger primitive and adopts D2's pre-built `ToolResult` convenience, so neither team rewrites call sites. A structural test asserts one acquire symbol. The required D2-spec deletions are named | §14, §12 A7, FR-019…FR-024 |
| **CRIT-005** — `controlledResult` and ~15 gateway sites still use the constant; the nominated guard cannot catch it | **Fixed, and the enumeration was larger than reported.** §2.2 counts **37** executable references (13 in `browser_ws.go`, not ~15, plus 24 elsewhere the review did not reach) plus 12 comments. FR-002b deletes the constant; FR-002c re-keys `controlledResult` and is assigned to **Stream A**, not Stream D. Regression list corrected: `shared_control_test.go` is **8** tests and is **not** the guard; `tools_control_test.go` (3 tests, `:59/:106/:153`) is | §2.2, FR-002b, FR-002c, §10.1, SC-009, SC-013 |
| **CRIT-006** — unattended contexts have no disposal path | **Re-scoped by the D1.2 ruling, and the underlying leak is closed — but this row's own evidence was false and is corrected here.** Sub-turns no longer create anything to leak (FR-009, FR-026c). ⚠️ **The previous wording of this row claimed, marked *verified*, that `ReapIdleSessions` "deletes `m.sessions` entries and never disposes a browser (its only removal is `delete(m.sessions, sessionID)`)". That was FALSE** (round-2 CRIT-102; ADR-072 §8 records it). It cancels `se.browserCancel` in both removal branches (`manager.go:3027-3032`, `:3073-3078`, executed `:3123-3125`), cancels per-tab contexts (`:3106-3107`) and reaches `coord.ReleaseTab` via `releaseGlobalTab` (`:3118` → `:3358-3365`). **The narrow true claim:** it never calls `RemoveAgent` or `disposeBrowserContextRaw`, so the Chrome **process** and the coordinator's per-key state are untouched. Whole-Chrome idle close is therefore still new work (FR-040), but it composes with an existing disposal path rather than filling a void — which is why FR-040a now specifies the reaper↔pool contract instead of assuming there is nothing to contract with | FR-009, FR-026c, FR-040, **FR-040a**, tests 31 + 41 + **55** |
| **MAJ-001** — the ladder cannot be evaluated in the order it specifies | **Fixed by deletion.** The step that could not run first (the attendance check) is withdrawn. The ladder is now three unambiguous rungs with no forward reference | §3.1 `resolve.go`, FR-007 |
| **MAJ-002** — "attended" is a proxy that does not implement the ruling | **Moot.** The ruling it failed to implement was itself reversed; there is no attendance concept | §0.2, §12 A2/A3 |
| **MAJ-003** — a fresh install has no workspaces | **REJECTED on the central claim; residual accepted.** *"Nothing seeds a workspace on a fresh install"* is false: `ensureDefaultWorkspace` (`pkg/gateway/rest_workspaces.go:468`) runs on **every** boot (`pkg/gateway/gateway.go:5013`) and creates "My Workspace" with `defaultWorkspaceTeam(cfg)` = `coreagent.All() ∩ configured agents` (`pkg/gateway/rest_workspace_delegation.go:359-379`) — which includes **Jim and Ray**, the two browser-policy-allowed agents. A fresh install resolves. **Accepted residual:** a *custom* agent is deliberately never auto-added to a team (`gateway.go:5018-5025`), so it resolves to nothing — and the panel's message for that case is misleading. FR-008a and US-14 fix the message; no workspace seeding is added | §6, US-14, FR-008a |
| **MAJ-004** — the "no wire change" claim passes only because SC-007 measures shape | **Fixed.** The claim is restated honestly; SC-007 gains a third, **behavioural** condition (test 46) that fails if `session_id` is not binding; the two replacement descriptions must match FR-016's verbatim text; `BrowserInspectRequest` is decided explicitly (its `session_id` is a *browser* session id, so it gains no chat semantics and resolves from the agent under FR-033). The `browser_started`→`state` persisted-JSON question is confirmed safe — `grep -rn "browser_started" src/` returns nothing | SC-007, US-10/AC3+AC4, FR-016, §6 SPA |
| **MAJ-005** — ADR criterion 3b has no automated coverage | **Fixed by (a) + (b).** FR-014a strengthens the artefact so the denial for a `browser_*` tool **names the browser surface** rather than the system-wide generic `"Tool execution denied by policy."` (`tool_denial.go:206-210`), and test 10 asserts that string. Holdout 4 is promoted to a **required UAT with a recorded transcript** (US-8/AC3), and §11 says plainly that no automated test observes what the model says | FR-014a, US-8, test 10, §13 holdout 4 |
| **MAJ-006** — the boot warm path is broken and unmentioned | **Fixed.** `pickWarmBrowserManager` (`gateway.go:3373`, called `:3562`) added to §2.1 as **modifies**; FR-016b requires it to warm the resolved key and to skip with a single INFO (not WARN) when nothing resolves; test 24 | §2.1, FR-016b, test 24 |
| **MAJ-007** — the capture registry has scope but no requirement | **Fixed.** FR-016a re-keys `captureRegistry` (`browser_webrtc.go:70-78`) to the browsing key and states the consequence: **one capture session per workspace browser**, and ADR-048's "requesting agent" conflict rule collapses because agents no longer have disjoint tab sets | FR-016a, test 25, §6 |
| **MAJ-008** — the leased set is a closed enumeration that D2 will grow | **Fixed, and this row's own count was stale — corrected (round-2 MAJ-101).** FR-019a replaces the enumeration with a rule plus a registry-**enumerated behavioural** test. **The exemption is a closed set of SIX, not three** (§14 rule 3 holds the normative counts); D2 adds **six** tools, of which **four** are leased automatically by the rule and two are exempt. This row previously said "three" while §14 said "five" and §14.1 said "five D2 tools" — a disposition table asserting a fix the annex had since changed | FR-019a, §14 rule 3, test 18 |
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

## 16. Round-2 review disposition (all 29 findings of `…-spec-review-round2.md`)

Every code claim below was re-derived from source on this worktree on 2026-08-31 rather than taken from the review — which is the practice CRIT-102 exists to enforce. Where the review's own evidence is wrong or narrower than stated, this table says so with the counter-evidence.

### CRITICAL

| ID | Disposition | Where / evidence |
|---|---|---|
| **CRIT-101** — §10.1's "unmodified" vs FR-002b's deletion; 364 unbudgeted test references | **ACCEPTED IN FULL. Both numbers reproduce exactly.** `grep -rc … --include "*_test.go"` returns **364 across 25 files**; the four files §10.1 named hold **115** between them (41/33/26/15). §10.1's bar is rewritten from *unmodified* to **semantics unmodified**, with a five-point diff standard; FR-002e budgets the migration, assigns it to **Stream A** (not Stream G, because it is a compile dependency of FR-002b), requires it in the **same commit** as the deletion, and forbids a test-only alias; test 5 and SC-013 go repo-wide including `_test.go` so the alias loophole is measurable | §2.2a, FR-002e, §10.1, test 5, SC-013 |
| **CRIT-102** — the `ReapIdleSessions` claim is false, twice, marked *verified* | **ACCEPTED IN FULL, and the underlying gap is now specified rather than assumed away.** Re-verified: `reapedBrowsers` collects `se.browserCancel` at `manager.go:3027-3032` and `:3073-3078`; `cancelBounded(rb.cancel, …)` executes at `:3123-3125`; per-tab cancels at `:3106-3107`; `m.releaseGlobalTab()` at `:3118` → `coord.ReleaseTab(agentID)` at `:3364`. Both occurrences corrected (§2.1's row and §15's CRIT-006 row) to the narrow true claim: it never calls `RemoveAgent` or `disposeBrowserContextRaw`, so the **process** and the coordinator's per-key state are untouched. **The consequence the review drew is real and is now a requirement:** FR-040a specifies the reaper↔pool contract — who closes, in what order, what happens to `browserMgrs` and the manager, and how the next call relaunches — and test 55 + a §10.2 row cover "reaper cancels `browserCancel` while the pool entry is live". The word *verified* is no longer used as a shortcut anywhere (§0's citation-policy note) | §0 citation policy, §2.1, §15 CRIT-006, FR-040a, test 55, §10.2 |
| **CRIT-103** — `ErrBrowserPoolFull`'s remedy does not work | **ACCEPTED IN FULL; option (a) taken, eviction still refused.** Both named actions were ineffective — closing a panel only calls `ViewerDetached`, and FR-040 requires zero tabs **and** zero viewers, so N workspaces holding tabs made the (N+1)th refusal permanent. §5 forbids eviction and that stands (a silent logout mid-task is worse than a refusal). **FR-046 adds the missing operator action** — `POST /api/v1/workspaces/{id}/browser/close` plus its SPA control, idempotent, viewer-notifying, profile-preserving — and the error text is rewritten as a formatted error naming the cap's **value**, the close action and the reload-without-restart raise. US-15/AC4, US-18, SC-018 and test 60 all use the **hard** case (every browser pinned by a tab **and** a viewer), because the existing cap test uses idle browsers and cannot fail for this | FR-039, FR-046, §3.1, US-15/AC4, US-18, SC-018, tests 59–60 |
| **CRIT-104** — the exempt set omits `browser_list_tabs`; the count appears four ways | **ACCEPTED IN FULL, and the deeper problem is fixed rather than papered over.** `browser_list_tabs` is registered (`register.go:76`), read-only, and its own file says it is not control-gated (`tabs.go:20`) — under the old AC5 it would have taken the write lease and deferred the headline demo behind an unrelated agent. It is now exempt; the set is **six**; §14 rule 3 carries the single normative count and the other three sites derive from it. **On "membership is a rule, not a list":** the review is right that the old rule was self-contradictory (`browser_handle_dialog` mutates *and* was exempt), so a rule was found that actually holds — **lease iff control-gated** — which partitions the eleven shipped tools exactly, with per-tool evidence in §14 rule 3's table, makes `browser_snapshot` fall out with no exception, and makes `browser_handle_dialog` exempt from **both** gates for one stated reason. Test 18 becomes behavioural (each tool exercised under a held control lock and a held write lease; the answers must agree) so it can no longer be made green by editing this document | §14 rule 3, US-9/AC4+AC4a+AC5, FR-019a, §12 A17, test 18, §16 MAJ-101/102 |

### MAJOR

| ID | Disposition | Where / evidence |
|---|---|---|
| **MAJ-101** — the exempt-set size and D2 tool count stated four ways | **ACCEPTED.** §14 rule 3 now opens with a normative-counts block (**exempt = 6; leased = 11 = 7 shipped + 4 of D2's 6**); §15's MAJ-008 row is corrected from "three"; §14.1's `leaseWrite` comment from "five new D2 action tools" to four, naming the two exempt ones | §14 rule 3, §14.1, §15 MAJ-008 |
| **MAJ-102** — the rule contradicts its own exemption; the list was widened under pressure | **ACCEPTED.** See CRIT-104. Additionally, the unspecified interaction the review names is now specified: after `browser_handle_dialog` clears a modal, the resuming leaseholder **must re-verify its own precondition** (re-resolve the selector, re-check the URL) rather than completing against a page that changed underneath it — §12 A17 | §12 A17, §14 rule 3 |
| **MAJ-103** — profile-dir nesting; `cleanStaleSingletons` cleans only `cfg.ProfileDir` | **ACCEPTED, and it turned up a second defect the review did not name.** Verified: `cleanStaleSingletons` (`coordinator.go:1488-1498`) is called with `c.cfg.ProfileDir` only (`:1235`); the default ProfileDir is itself a user-data-dir (`manager.go:125`). **Decided:** `cfg.ProfileDir` keeps its meaning, per-key dirs are **siblings** under `profileRoot = filepath.Dir(cfg.ProfileDir)`, and FR-042b runs `cleanStaleSingletons` against each per-key dir before launch. **The second defect:** `InstallRootForProfileDir` (`exec_resolver.go:50`) is arithmetic over the *parent* of what it is given, so feeding it a per-key path yields a different, wrong managed-Chromium install root **per workspace** — N downloads of one binary. FR-037a resolves the exec path once from `cfg.ProfileDir`; §5 forbids the per-key call; test 52 asserts it | FR-037a, FR-042b, §3.1 INVARIANT P-5, §5, US-19/AC3, tests 52 + 57 |
| **MAJ-104** — boot `Preprovision` silently breaks under a lazy pool | **ACCEPTED IN FULL.** Verified: `gateway.go:2286` ranges `agentLoop.BrowserManagers()` per manager to call `Preprovision`; `BrowserManagers()`' own doc states the purpose is that *"a fresh install's managed Chromium download starts at boot instead of at an agent's first browser tool call"*. Under a lazy pool that snapshot is empty at boot. **Cleanly fixable, and the code says so:** `Preprovision`'s entire body is `if m.cfg.CDPURL != "" { return "", nil }; return m.resolveExecPath(ctx)` (`manager.go:3218-3223`) — no key, no manager, no live Chrome. FR-016c moves it to `pool.Preprovision(ctx)`, called once at boot; test 61 asserts it fires with zero live keys | FR-016c, §2.1, BDD *boot-preprovision*, test 61 |
| **MAJ-105** — `metadata.go`'s second construction; test 4's predicate would trip on three non-tool holders | **ACCEPTED IN FULL, plus a fourth holder.** Verified: `metadata.go:36-51` builds all eleven structs again with a nil manager — added to §2.1 as **modifies**, with test 4a. Verified the three non-tool holders (`CaptureSession` `capture_session.go:258`, `LiveViewRegistry` `live.go:322`, `LiveView` `live.go:1324`) and found a **fourth**, `BrowserCoordinator.managers` (`coordinator.go:186`). Test 4's predicate is restated precisely as *every type in the package implementing `tools.Tool`*, and all four legitimate holders are named in §2.1 so nobody "fixes" them. **Bonus finding:** `LiveViewRegistry`'s doc comment (`live.go:316-320`) asserts *"a BrowserManager is itself scoped to one agent"* — false after the re-key, corrected with FR-002d | §2.1, test 4 note, test 4a, FR-002d |
| **MAJ-106** — both gates are decorative; G-2's is a skip that reports green | **ACCEPTED IN FULL; the two gates are separated and given different, honest enforcement.** Verified `skipIfNoBrowser` has **two** green-skip paths, not one: CI without `OMNIPUS_BROWSER_E2E` (`browser_e2e_test.go:66-68`) **and** no probeable Chrome anywhere (`:69-111`, via `resolveTestBinary`'s own `t.Skipf`). **G-2 becomes mechanical:** `requireBrowserOrFail` instead of `skipIfNoBrowser`, the gate job sets the env var, the receipt is captured without a pipe and asserted to contain no `--- SKIP` and no `no tests to run` — four conditions in SC-012a, any of which failing fails the gate. **G-1 is declared a HUMAN gate and its owner is named** (the implementing PR's reviewer), because the mechanical form the review suggested — a test asserting a measurement file is non-empty and dated — passes on a fabricated file and would become a second place to write the guess. SC-012 names the artefact (raw RSS for N=1…4, host RAM, and the arithmetic to the default); test 51 is the partial mechanical half and its limits are stated | §0.3.1, FR-044, FR-045, SC-012, SC-012a, tests 37 + 51 |
| **MAJ-107** — Stream C ships the isolation claim before Stream P delivers isolation | **ACCEPTED IN FULL.** The review's diagnosis is exact: the justification was true of the cookie behaviour and false of the sentence describing it, and `capture_shared_context: true` (`defaults.go:671`) keeps one partition across all workspaces until P lands. **Also discovered while fixing it: FR-034 claimed the replacement literals were "specified here" and the previous draft specified none of them anywhere.** New **§3.3** writes both stages verbatim: interim (Stream C) *"the browser this workspace's agents share"* — claims only tab-set sharing, true after Stream A; final (FR-034a, Stream P, **same commit as FR-037**) *"this workspace's browser"* plus an explicit per-workspace-logins sentence. §5 adds the general non-behaviour. **This also answers the review's unasked question 10** (what is the rollback if G-2 fails after §0.4 lands): nothing, because nothing in §0.4 asserts isolation any more | §0.4, §3.2, §3.3, FR-034, FR-034a, §5 |
| **MAJ-108** — the idle window is named five times and defined nowhere | **ACCEPTED IN FULL.** Config key **`tools.browser.idle_close_ttl`**, default **15m** with its derivation stated (3× the per-tab `idle_ttl` at `manager.go:134`, and why the asymmetry is right: per-tab reaping already reclaims the renderers, the dominant cost). **Caller named:** the existing 1-minute sweep goroutine (`gateway.go:5321-5352`), after its `ReapIdleSessions` loop — a caller the previous draft never identified. **Post-close state specified, and the tension the review found is resolved by naming it:** FR-026a's liveness is about the **key** (does this workspace still warrant a browser), the pool's is about the **process**; they are different questions, so a live key with no running Chrome is a legal, described state. `browserMgrs` entry and `*BrowserManager` survive; `pool.Acquire` relaunches from the profile; login intact | FR-040a, §12 A22, US-12/AC4a, BDD *idle-close-relaunch*, tests 54 + 55, §10.2 |
| **MAJ-109** — `max_browsers` has no edge semantics and no stated relation to the tab budget | **ACCEPTED IN FULL.** Verified `maxTotalTabs <= 0` means unlimited (`coordinator.go:785-788`). **(a)** `max_browsers <= 0` means unlimited too — same shape, so an operator who knows one key is not surprised by the other. **(b)** `max_total_tabs` stays **global** across all N Chromes; making it per-Chrome would silently multiply a configured ceiling by N. The starvation consequence is stated: it is today's behaviour across agents and the pool does not worsen it. **(c)** on the default being measured on one box — that is exactly what FR-044/G-1 is, and §12 A21 states plainly that it is a fixed conservative constant operators on larger hosts must raise, rather than a function of host memory (auto-sizing from RAM would need its own measurement to be honest, and G-1 has not run yet) | FR-038a, §12 A21, US-15/AC5+AC6, test 53, §10.2 |
| **MAJ-110** — no boot reconciliation; orphan Chromes sit outside the cap | **ACCEPTED, with the shipped behaviour described accurately.** The review implies orphans are silently ignored; in fact the marker is consulted **at launch** and the shipped path **refuses to launch** when a marker's pid is alive (`coordinator.go:1448-1467`) — a reasonable single-Chrome story that is not a boot-time story. The review's real point stands: orphans consume the host memory the cap exists to bound while `LiveKeys()` cannot see them. FR-042a adds the boot pass (dead pid ⇒ remove marker + stale lock, INFO with a count; live omnipus-owned pid ⇒ terminate, WARN with workspace and pid). **§12 A20 records what the review did not raise:** pid reuse is a real hazard, the shipped code has it too, and the `/proc/<pid>/exe` mitigation is **Linux-only** — on macOS the marker is removed without terminating and a WARN names the pid, a smaller guarantee stated rather than implied | FR-042a, §12 A20, §6, US-19, SC-016, test 56, §10.2 |
| **MAJ-111** — the profile directory has creation but never deletion | **ACCEPTED IN FULL; decided as delete-on-workspace-deletion.** FR-043a: **workspace deletion is the sole trigger**, after `pool.Close(k)` returns; idle close, roster change, reload, operator close and crash recovery all leave it — five negative cases in test 58, because the positive case alone would pass a "delete always" bug. Directory mode **0700**, stated rather than inherited (matching `coordinator.go:1232`, `manager.go:799`), because these now hold per-client session cookies. **No quota, and the reason is given** rather than omitted: live profiles are bounded by `max_browsers`, and dead ones are removed by the deletion path, so the unbounded case the review worries about is closed by deletion rather than by a ceiling. A release-note line is required | FR-043a, US-20, SC-017, test 58, §5 |
| **MAJ-112** — `leaseWaitTimeout`'s relationship to the action-tool timeout is a comment, not a gate | **ACCEPTED, with one correction to the finding.** The timeout is `BrowserConfig.PageTimeout` (`manager.go:35`, default 30s `:123`) as the review says — but the config key is **`tools.browser.page_timeout`**, not `page_timeout_sec`; `PageTimeoutSec` is the Go field name (`config.go:3632`, env `OMNIPUS_TOOLS_BROWSER_PAGE_TIMEOUT`, applied `loop.go:2311-2312`). FR-023a **clamps** rather than rejects — aborting boot over a browser tuning key is disproportionate, and a clamp preserves the contract the operator cares about — with a WARN naming both keys and both values, at load **and** on reload. The WARN is part of the requirement: a silent clamp leaves the operator believing a setting took effect | FR-023a, §14.1, §6, test 50, §10.2 |
| **MAJ-113** — FR-033's premise is contradicted by shipped code; Ray's heartbeats lose the browser | **PARTIALLY ACCEPTED — the general point is right, the stated consequence is WRONG, and the correction matters because it is the finding's whole force.** Accepted: the stamping path is real (`loop.go:6934-6957`) and the previous draft cited neither it nor `resolveWorkspaceIDForContinuation`; it is now cited in §6, US-6/AC0 and §12 A19. **Rejected: "adding Ray to a second workspace permanently kills browsing on all his heartbeats."** Heartbeat jobs are workspace-scoped **by construction** — the reconciler names each `heartbeat:<workspaceID>:<agentID>` (`heartbeat_schedule.go:30-33`), `resolveScheduleWorkspaceID` parses the workspace back out (`schedules.go:639`, `:654`), and `pickSession` stamps it on every fire (`:513`, `:527-576`, called `:141`). So a heartbeat reaches **rung 1** and never sees FR-033; enabling a heartbeat on a second workspace creates a *distinct job with its own workspace*, not an ambiguity. **The real residual is narrower and is now stated:** a *plain, operator-created* schedule resolves to `""` (ADR-065 FR-8 removed the channel source, `schedules.go:632-639`), so a plain schedule for a multi-workspace agent **is** refused — the one case where "which client's logins?" genuinely has no answer. The suggested alternative (a per-agent browsing-home workspace) is **declined** in §12 A19: ADR-037 removed the global delegation graph precisely because "the owner's workspace" is ambiguous for a multi-workspace agent, and `resolveScheduleWorkspaceID`'s own doc rejects `CronJob.AgentID` on the same grounds; re-adding a global agent attribute for browsing would reverse that | §6, US-6/AC0, §12 A19, §2.1 |
| **MAJ-114** — ADR D2.11's elevation-of-privilege disclosure is owned by nobody | **ACCEPTED IN FULL; CLAIMED, not handed off.** ADR D2.11 **decides** it (*"the team-editing UI must state this at the point of adding, not only in release notes"*), §1's out-of-scope list excluded only the *information-disclosure* bullet, and D1.2 makes it worse — unattended delegated work now inherits those logins. FR-047 adds it: Workspace → Team, visible **before** confirmation, naming the consequence rather than the mechanism, with the same text in the release note. §1's out-of-scope wording is tightened so the split between D2.11's three bullets is explicit (elevation → FR-047 here; repudiation → FR-027 here; information disclosure → D2) | FR-047, US-21, §1, test 64 |

### MINOR

| ID | Disposition | Where / evidence |
|---|---|---|
| **MIN-101** — the comment count is wrong and the grep does not produce the quoted number | **ACCEPTED IN FULL; reproduced exactly.** The command returns **57**: 37 usages + **2** declarations (`tools.go:63`, `:69`) + **18** comments (the draft said 12). All 18 are enumerated in §2.2, including the six the draft missed (`tools.go:50,62,65,68`, `capture_session.go:708`, `config.go:3892`), and `pkg/config/config.go` is added to the inventory with a note that it is comment-only. Re-verified independently: **all 37 executable line numbers are correct** | §2.2 |
| **MIN-102** — the test-side surface is unbudgeted | **ACCEPTED.** See CRIT-101; §2.2a carries the full per-file table and §10.1 the migration bar | §2.2a, §10.1 |
| **MIN-103** — the `"ws:"` prefix and `WorkspaceID()` are residue of `BrowsingKeyKind` | **ACCEPTED as "explain it or drop it"; kept, with the explanation the review asked for.** The prefix is retained deliberately as a **namespace marker**, not as future-proofing for a second shape: workspace ids are bare ULIDs, and a bare ULID in an audit event, a WARN or a `LiveKeys()` dump is indistinguishable from a session, task or agent id — all ULIDs, all appearing in the same lines. `WorkspaceID()` stays because two consumers need the bare id (the profile path, the audit field). Written into `key.go`'s doc comment so it is not unexplained next to a non-behaviour forbidding a second shape | §3.1 `key.go` |
| **MIN-104** — the `sandbox_apply.go` span is wrong | **ACCEPTED.** Verified: the DevTools-port removal NOTE is at `:412-417`; `:405-409` is the `enforcePortRules` computation (`:407` `KernelChildConfiner`, `:408` the ABI check). Both citations corrected | §0.1, §6 |
| **MIN-105** — a runtime `ProfileDir` change relocates every profile and the spec is silent | **ACCEPTED; resolved by preserving shipped behaviour rather than inventing one.** Verified `coordinator.go:681-687`: today the change logs a WARN and is **not applied** to a running Chrome (*"applies after gateway restart"*). §12 A18 keeps exactly that per key — not applied live, WARN now names the affected key count, paths re-derive at the next restart, and the resulting logout is stated in the WARN and in the config key's doc. Live relocation was rejected: hundreds of MB per workspace with Chrome holding files open | §12 A18, §2.1 |
| **MIN-106** — a workspace id becomes a path segment with no stated constraint | **ACCEPTED IN FULL.** Verified ids are server-minted ULIDs (`rest_workspaces.go:495` default, `:848` created). The invariant is now written down **and enforced** — `filepath.Base(id) == id`, not `.` or `..` — checked in `ResolveBrowsingKey` before the key exists, refusing as `ErrNoBrowsingContext`; added to §5 as a non-behaviour, with test 62 and a §10.2 row | §5, FR-037, test 62, §10.2 |
| **MIN-107** — the residual custom-agent case already has a shipped boot-time surface | **ACCEPTED IN FULL.** Verified `logWorkspacelessAgents(homePath, cfg)` at `gateway.go:5026`, immediately after `ensureDefaultWorkspace` at `:5013`, with the rationale at `:5015-5025`. §6 now requires FR-008a's panel reason and `ErrNoBrowsingContext`'s text to name the **same** remedy in the **same** words as that log line, so an operator seeing both does not think they are two problems | §6, US-14, FR-008a |
| **MIN-108** — ADR-072 D1.3's table still calls the transcript session id "used for unattended delegated work" | **ACCEPTED, NOT ACTIONED HERE — it is an ADR edit, not a spec edit.** Verified the stale row is still in D1.3. §2.1 correctly marks that key **not used**, so the ADR and its spec disagree and the ADR is the source of record. Noted at the end of §12 so it is not lost; the ADR's owner amends it | §12 corrections note |

### OBSERVATION

| ID | Disposition | Where / evidence |
|---|---|---|
| **OBS-101** — the four "red today" tests are plausible but no receipt is offered; SC-008 is an intention | **ACCEPTED.** The review is right that all four are expressible against current code, so confirming them costs four commands. SC-008 is rewritten with an explicit failure condition: **the four `exit=` receipts must be pasted into the PR *before* the implementing commits**, captured without a pipe. A red-then-green claim made after the fix has landed is not reproducible and does not satisfy the criterion | SC-008 |
| **OBS-102** — recording that MAJ-003's rejection is correct so it is not re-litigated | **NOTED, no action.** Re-verified independently: `ensureDefaultWorkspace` (`rest_workspaces.go:468`) runs on every boot (`gateway.go:5013`) and seeds the built-in roster via `defaultWorkspaceTeam`. The scope note is right and is already reflected: the default workspace is covered, and the **custom** agent (US-14) is the right residual concern — now also wired to the shipped `logWorkspacelessAgents` surface per MIN-107 | §15 MAJ-003, §6, US-14 |
| **OBS-103** — the surviving new concepts are defensible; no action | **NOTED, no action.** Recorded here so a later reader sees the observation was read rather than skipped. `BrowsingKey`, `TabState`, `BrowserResolveOutcome`, `ManagerResolver` and `BrowserPool` all survive this revision unchanged in shape; this round adds no new type beyond the pool's own methods (`CloseIdle`, `DeleteProfile`, `ReconcileMarkers`, `Preprovision`), each of which owns one FR | §3.1 |

**Round-2 tally: 29 findings — 25 accepted in full, 3 accepted with a correction to the finding's own evidence (MAJ-110, MAJ-112, MIN-108's ownership), 1 partially rejected with counter-evidence (MAJ-113's stated consequence).** Three findings turned up defects the review did not name: MAJ-103's install-root arithmetic (FR-037a), MAJ-105's fourth manager-holding struct and `LiveViewRegistry`'s stale doc comment, and MAJ-107's discovery that FR-034's replacement literals were never actually specified anywhere (§3.3).

---

**Next:** run gate **G-2** under §0.3.1's four conditions and gate **G-1** with SC-012's artefact; land **Stream A** — including the §2.2a 364-reference migration in the same commits as FR-002b — plus the rest of the §0.4 set, with FR-046 and FR-047 among it; then build **Stream P**, with FR-034a's final description literals in the same commit as FR-037. The D2 spec must be edited to delete its lease and reference §14 before either spec is implemented, and must register `browser_handle_dialog` ungated by `controlledResult` (§12 A17) or that exemption rule reverts to a list. **SC-015 is satisfied** — ADR-072 D1.1a names Daniel Piatkowski as decider for every ruling in the ADR. **One ADR edit is outstanding and belongs to the ADR's owner:** D1.3's key table still describes the transcript session id as "used for unattended delegated work", which the D1.2 ruling deleted (§16 MIN-108).
