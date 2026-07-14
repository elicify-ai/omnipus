# Adversarial Review: Browser shared Chrome + per-agent contexts (ADR-043 spec)

**Spec reviewed**: `docs/internal/specs/browser-shared-chrome-spec.md`
**Review date**: 2026-07-14
**Review round**: 2 of 2 (FINAL — gates the fan-out build)
**Verdict**: **FAIL (BLOCK)** — one CRITICAL

## Executive Summary

Round 1's two CRITICALs were folded in by intent, but the headline fix —
CRIT-002 ("re-adopt context by agentID across reload; do NOT dispose on reload") —
is **defeated by chromedp's documented lifecycle** and is therefore unimplementable
as the spec describes. Verified against `chromedp@v0.15.1`: a browser context
created with `WithNewBrowserContext` is **owned** by the Go `context.Context` that
created it (`browserContextOwner = true`, chromedp.go:372), and chromedp's cancel
goroutine auto-disposes it when that context is done
("The new BrowserContext will be disposed when the context is done", chromedp.go:495-496;
dispose call at :205-210). The spec places context creation on the **per-agent
manager** (Stream B:94 — "managers get a connection, then create their context on
it"), and the manager's chromedp context is canceled on **every** reload by
`prior.Shutdown()` (loop.go:1711-1713). Result: cookies/login/localStorage are
destroyed on every Settings save — the precise incident CRIT-002 was meant to
prevent, returning through chromedp's back door. The re-adoption primitive the
fix needs (`WithExistingBrowserContext`, chromedp.go:511-518) exists and is
non-owning — but the spec never assigns the *owning* role to the long-lived
coordinator, so the mechanism never engages.

Beyond that, four MAJORs survive: (a) "tabs preserved" across reload is also
false (chromedp closes the manager's targets on cancel); (b) the coordinator's
locking discipline is unspecified and the tab-budget check-then-open is
non-atomic (the "exactly one wins" dataset is unimplementable); (c) the §10
crash dataset still says "cookies intact", contradicting CRIT-001's resolution;
(d) SC-002's "<4 GB RSS" has no measurement methodology. Round-1 MIN-001/002
(FR-007 no US; FR-008/FR-010 no BDD) were only partially addressed and remain.

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 5 |
| MINOR | 4 |
| OBSERVATION | 2 |
| **Total** | **12** |

---

## Findings

### CRITICAL

#### [CRIT-003] chromedp auto-disposes a `WithNewBrowserContext` context when its owning Go context ends — the spec's "manager creates its context" design silently destroys cookies on EVERY reload (CRIT-002 fix is unimplementable as written)

- **Lens**: Infeasibility + Incorrectness (verified against code)
- **Affected section**: §3 interface contract (lines 60-68), §3 Stream A invariants C1.2 (line 88), §3 Stream B (line 94), §4 behavioral contract (line 115), US-3/AC1 (line 145), hot-reload BDD (line 174)
- **Code evidence** (all `chromedp@v0.15.1`):
  - `chromedp.go:494-507` — `WithNewBrowserContext` docstring: *"The new BrowserContext will be disposed when the context is done."*
  - `chromedp.go:364-374` — the create path sets `c.browserContextOwner = true`.
  - `chromedp.go:205-210` — the cancel goroutine: `if c.browserContextOwner { action := target.DisposeBrowserContext(c.BrowserContextID); ... }`.
  - `chromedp.go:511-518` — `WithExistingBrowserContext(id)` sets `c.BrowserContextID = id` and does **not** set `browserContextOwner` → re-adopters are non-owners and do NOT dispose on cancel. (Re-adoption primitive exists and is correct.)
  - `pkg/agent/loop.go:1711-1713` — reload path: `prior.Shutdown()` then `al.browserMgrs[agentID] = mgr`.
  - `pkg/tools/browser/manager.go:2070-2091` — `Shutdown()` cancels the manager's chromedp context (`m.allocCancel`).
  - `pkg/tools/browser/manager.go:1094-1105` — `bootstrapBrowserCtx` is where `WithNewBrowserContext()` is to be added (Stream B owns it).
- **Description**: The spec says the **manager** creates its browser context (Stream B line 94: "managers get a connection, then create their context on it") via `WithNewBrowserContext()` (§3 Stream B line 93). It also says the manager's `Shutdown()` "closes the manager's DialCtx and, on the RELOAD path, does NOT dispose the browser context" (Stream A invariant C1.2, line 88). These two statements are **mutually incompatible with chromedp's contract**: the chromedp `context.Context` that calls `WithNewBrowserContext` becomes the context's **owner**, and chromedp itself — not Omnipus code — calls `Target.disposeBrowserContext` when that Go context is canceled. `prior.Shutdown()` on reload cancels the manager's chromedp context → chromedp disposes the browser context → cookies/localStorage/login for that agent are gone. The spec never calls a dispose method, yet disposal happens anyway. CRIT-002's fix is therefore correct in intent but unimplementable under the spec's ownership assignment.
- **Impact**: Identical to round-1 CRIT-002 — every Settings save while agents browse silently logs every agent out of every site. "3 AM: saved a display toggle, all three research agents lost their GitHub/Jira logins." The round-1 fix does not hold.
- **Recommended fix (mandate the ownership topology)**: The **coordinator** — whose instance survives reload (verified: `ReloadProviderAndConfig` is `func (al *AgentLoop)`, calls `registerSharedTools(al, …)` on the same `al`, loop.go:3288/3344; it does NOT reconstruct the loop) — must be the sole **creator and owner** of each agent's browser context. Concretely:
  1. On first `Register(agentID)`, the **coordinator** creates the context (its chromedp context is parented to a long-lived connection that is NOT canceled on reload) and records `agentID → browserCtxID`. The coordinator is `browserContextOwner`.
  2. Managers **never** call `WithNewBrowserContext`. They re-adopt via `chromedp.WithExistingBrowserContext(browserCtxID)` (non-owner → chromedp's auto-dispose does not fire on the manager's `Shutdown`).
  3. Context disposal is explicit and coordinator-only: on agent-removal or gateway `Close()` (`coordinator.Shutdown()`), the coordinator disposes (or cancels its owning context).
  4. Rewrite Stream B line 94 and §3 interface contract to state explicitly that **the coordinator creates+owns the context; managers re-adopt via `WithExistingBrowserContext`**. Add an invariant: "no `WithNewBrowserContext` call exists on a `*BrowserManager` code path."

---

### MAJOR

#### [MAJ-006] "Tabs preserved across reload" (US-3/AC1, hot-reload BDD) is also false — chromedp closes the manager's targets on context cancel

- **Lens**: Incorrectness (verified against code)
- **Affected section**: US-3/AC1 (line 145 — "logins/tabs preserved"), hot-reload BDD (line 174), §4 (line 115)
- **Code evidence**: `chromedp.go:188-204` — the same cancel goroutine, for non-first targets, calls `target.CloseTarget(c.Target.TargetID)`. Tabs are live renderer targets owned by the manager's chromedp context; canceling it (reload) closes them.
- **Description**: Even with CRIT-003 fixed (cookies survive because the browser-context storage persists keyed by `browserContextId`), the agent's **open tabs** do not survive — chromedp closes them when the manager's chromedp context is canceled. So US-3/AC1's "tabs preserved" and the BDD's implication that the same tab/pages persist are wrong. What does survive: cookies/localStorage/indexedDB (browser-context-scoped storage). The agent must reopen tabs; pages reload to their URLs (still logged in, because cookies persist).
- **Recommended fix**: Reword US-3/AC1 and the hot-reload BDD to "logins/localStorage preserved; tabs are reopened (each page reloads, still authenticated)." The "still-logged-in state" assertion in the BDD is fine **after** a fresh `browser_navigate` to the URL — make that navigate explicit in the BDD's Then-step.

#### [MAJ-007] Coordinator locking discipline is unspecified; the tab-budget check-then-open is non-atomic (target 4 + the "exactly one wins" dataset is unimplementable)

- **Lens**: Incompleteness + Infeasibility (concurrency correctness)
- **Affected section**: §3 interface contract (lines 56-82), §3 Stream A crash detection ("serialized by the coordinator's own mutex"), §3 Stream D (line 104), §5 non-behavior line 126 (the ADR-038 "no lock across CDP" rule applies to the manager, not the new coordinator), §10 dataset "2 agents race browser_open_tab … exactly ONE succeeds" (line 245)
- **Description**: Two distinct gaps.
  1. **Launch serialization vs ADR-038.** The spec says the double-launch race is "serialized by the coordinator's own mutex." Launching Chrome is a **blocking CDP call**. If `Register` holds the coordinator mutex across the launch, N-1 concurrent `Register` callers block holding-nothing (fine) — but the spec never states whether the holder drops the mutex during launch (the ADR-038 anti-pattern, now at the coordinator layer). The manager's `ensureStarted` carefully releases `m.mu` across exec-path resolution (manager.go:511-526); the coordinator needs the same discipline spelled out, or an implementer will hold the lock across launch.
  2. **Budget check-then-open race.** Stream D's enforcement is "before opening, call `coordinator.TotalOpenTabs()` … and deny when ≥ max." `TotalOpenTabs` sums across managers (coordinator scope); the actual tab creation happens later inside the manager (manager scope, a separate `CreateTarget` CDP call). Two agents that both observe `< max` proceed concurrently and **both** open → budget violated. The §10 dataset (line 245) asserts "exactly ONE succeeds, the other is denied" — unimplementable without an atomic reservation spanning the coordinator↔manager boundary.
- **Recommended fix**: Specify the lock topology explicitly. (a) `Register` uses a `sync.Once`/condvar (or releases the mutex and blocks waiters on a channel) so the blocking launch runs **without** the coordinator mutex held — mirroring `ensureStarted`'s release-relock. (b) Make the budget a **reservation**: `coordinator.ReserveTab(agentID) bool` increments a single atomic counter under the coordinator mutex and returns false when it would exceed `max_total_tabs`; the manager calls it **before** `CreateTarget` and calls `ReleaseTab` on failure. This makes "exactly one wins" implementable. Add a BDD/dataset asserting the atomicity.

#### [MAJ-008] §10 test dataset still says "cookies intact" for the crash scenario — directly contradicts CRIT-001's resolution

- **Lens**: Inconsistency (carried forward; round-1 fix incomplete)
- **Affected section**: §10 test datasets, row "Chrome killed mid-session | recovered ≤10s, **cookies intact** | FR-005" (line 241)
- **Description**: CRIT-001 was resolved by rewriting US-4/AC1, the crash BDD, and §4 to "fresh empty contexts (cookies NOT intact)." But the §10 dataset table was not updated. The row for FR-005 still reads "cookies intact." An implementer writing `TestCoordinator_CrashRecovery` (R2) from the dataset will assert "cookies intact" — the opposite of the (correct) BDD — and either ship a wrong test or fail.
- **Recommended fix**: Change the row's Expected to "recovered ≤10 s into a **fresh empty context** (prior login absent — documented limitation)."

#### [MAJ-009] SC-002 "<4 GB RSS" has no measurement methodology (target 6)

- **Lens**: Infeasibility (unmeasurable success criterion)
- **Affected section**: SC-002 (line 251), §10 TDD test #10 `TestFiveAgents_ConcurrentStress` (line 224)
- **Description**: Round-1 MAJ-005 specified PID-based assertion mechanisms for R1/R2/R3. The headline SC-002 ("<4 GB RSS") got a numeric cap (good) but **no measurement path**. The shared Chrome is one browser process + N renderer children (per-tab, per the ADR's D6). How does `TestFiveAgents_ConcurrentStress` compute "total browsing RSS"? Summing only the parent pid's `VmRSS` misses the renderers (the dominant term, per D6). Summing descendants requires enumerating Chrome's child pids. No mechanism is specified, so the headline criterion is unimplementable as written.
- **Recommended fix**: Specify the read path: "Sum `VmRSS` (kB) from `/proc/<pid>/status` over the shared-Chrome pid **and all descendant pids** (walk `/proc/<child>/stat` `PPID` from the Chrome pid; re-scan once post-warmup and once at the 60 s mark; take the max). Assert max ≤ 4 GB." Note: the current pod has 15 GB / 9.3 GB free, so the test is feasible here; the <4 GB cap is for the 3.8 GB floor case — the existing "if pod <8 GB free, cap tabs per agent" clause is the right guard, keep it.

#### [MAJ-010] Stream D "parallel with B" is overstated for the test-level definition of done (target 3)

- **Lens**: Inconsistency (MAJ-001 only partially resolved)
- **Affected section**: §3 Stream D (line 105), §3 "Parallelization note" (line 110)
- **Description**: MAJ-001's resolution (the `OpenTabCount()` interface) makes D **compile** independently of B. But D's `TestOpenTab_GlobalBudgetDenial` needs a **concrete** `OpenTabCount()` to observe `≥ max_total_tabs`; until Stream B lands a real implementation, D's test can only run against a stub returning 0 — and "2 tabs open, 3rd denied" never triggers. So D's test-level DoD is serialised after B. The "B/C/D fan out in parallel" claim holds for code-writing, not for green tests.
- **Recommended fix**: State in Stream D that its unit test **injects a fake `TabCounter`/manager** (a tiny mock exposing `OpenTabCount()`) so the budget-denial logic is unit-testable the moment the interface lands — independent of B's real implementation. Keep the integration assertion (real managers) in Stream E.

---

### MINOR

#### [MIN-005] FR-007 still has no user story (carried: round-1 MIN-001 unaddressed)
- **Affected**: §9 traceability row FR-007, US column = "—" (line 206). Round 1 asked for US-8 (operator-facing "reject foreign Chrome on 9223"). Not added. Add it, or explicitly mark FR-007 a non-functional security requirement with a noted deviation from the structural rule.

#### [MIN-006] FR-008 and FR-010 still have no BDD scenarios (carried: round-1 MIN-002 only partially addressed)
- **Affected**: §9 — FR-008 BDD = "(R3)" (a test name, not Given/When/Then); FR-010 BDD = "—" (line 207-209). §8 has 7 BDDs; none covers FR-008 (only-Close-kills) or FR-010 (live-view binds on agent_id). Structural check "every acceptance scenario has a BDD" FAILs for US-6/AC1 (FR-010). Add a BDD for each (FR-008: reload+per-agent Shutdown leave PID alive, then Close() kills it; FR-010: a viewer attaching with `agent_id=Jim` sees Jim's active tab, not Ray's).

#### [MIN-007] Coordinator placement is still §12 "Assume / UNKNOWN" — load-bearing-open at a FINAL gate
- **Affected**: §12 ambiguity table line 259. CRIT-003 makes coordinator survival decisive (the `agentID → browserCtxID` map AND the context-owning chromedp connection must survive reload). "Assume the gateway holds a ref" is no longer adequate for a PASS — pick one (verified candidates that survive: a field on `*AgentLoop` since `ReloadProviderAndConfig` reuses `al`; or gateway-owned). State it as a decision, not an assumption.

#### [MIN-008] `Close()` path currently calls per-manager `mgr.Shutdown()`; new model must reconcile
- **Affected**: §3 Stream A "gateway Close() wiring" (line 86), `pkg/agent/loop.go:2785-2798`. The existing `Close()` iterates `al.browserMgrs` and calls each `mgr.Shutdown()` (which today cancels the allocator → kills Chrome). In the new model per-manager `Shutdown()` must NOT kill Chrome (connection+context only), so `Close()` must additionally call `coordinator.Shutdown()` (the sole process-kill path, D4 invariant 4). Stream A gestures at "gateway Close() wiring" but doesn't call out that the existing per-manager loop must become connection-only and a separate `coordinator.Shutdown()` added. Spell it out.

---

### OBSERVATIONS

#### [OBS-004] CRIT-001 "fresh contexts, cookies lost on crash" should be re-confirmed with the operator (target 2)
The operator profile is "**often simultaneous** scraping" — plausibly authenticated sites (GitHub, Jira, portals with 2FA/CAPTTCHA). Losing **every** login on **every** Chrome crash may make that profile operationally painful (agents cannot pass headless re-auth). The spec documents it as a limitation, which is honest; but given the profile, either (a) re-ask the operator to confirm acceptability as-is, or (b) promote the cookie-restore enhancement (`Network.getCookies` snapshot → `Network.setCookie` replay on relaunch) from "future" (line 89) to a near-term follow-up issue. Not a blocker — a flagged re-ask.

#### [OBS-005] Pod reality check (informative, not a finding)
Current pod: 15 GB RAM / 9.3 GB free / 0 swap / disk 90 %. The 5-agent stress test (SC-002) is feasible here; the <4 GB RSS cap is correctly sized for the 3.8 GB floor case (defensive). The 90 %-full root disk is the larger near-term hazard for the build (Go build cache can push it over) — unrelated to the spec, but worth a `df -h /` watch during the stress run.

---

## Did the round-1 fixes actually hold?

| Round-1 finding | Folded in? | Holds under round-2 scrutiny? |
|---|---|---|
| CRIT-001 (crash → fresh contexts, cookies lost) | Yes — US-4/BDD/§4 reworded | **Partially** — the §10 dataset still says "cookies intact" (MAJ-008) |
| CRIT-002 (reload re-adopts by agentID; no dispose) | Yes — intent captured in §4/US-3/interface comments | **No** — unimplementable as written; chromedp auto-disposes (CRIT-003) |
| MAJ-001 (TotalOpenTabs cross-stream dep) | Yes — `OpenTabCount()` interface added | **Mostly** — compile-independent; test-independent only with a mock (MAJ-010) |
| MAJ-002 (GetOrConnect lacks agentID) | Yes — `Register(ctx, agentID, mgr)` | **Yes** |
| MAJ-003 (D2 spike no fallback) | Yes — stop-work fallback added (line 96) | **Yes** |
| MAJ-004 (SC-002 undocumented cap) | Yes — "<4 GB" + floor clause | **Mostly** — cap present, but no RSS measurement method (MAJ-009) |
| MAJ-005 (R1-R3 mechanisms) | Yes — PID()/killCount specified | **Yes** (but SC-002 RSS mechanism missing — MAJ-009) |
| MIN-001 (FR-007 no US) | No | **No** — MIN-005 |
| MIN-002 (FR-008/009/010 no BDD) | Partially — FR-009 got a BDD via US-7 | **No** for FR-008/FR-010 — MIN-006 |

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| Every US has acceptance scenario | PASS | US-1..US-7 |
| Every AC has a BDD | **FAIL** | US-6/AC1 (FR-010) has no BDD — MIN-006 |
| Every BDD has `Traces to:` | PASS | all 7 BDDs (§8) |
| Every BDD has a TDD test | PASS | |
| Every FR in traceability | PASS | FR-001..010 |
| Every FR has a US | **FAIL** | FR-007 US = "—" — MIN-005 |
| Datasets cover boundaries/edges/errors | PARTIAL | added: empty ctx, budget race, crash+immediate-call. Missing: 0 agents (lazy vs boot launch — unasked Q8 still open), `max_total_tabs=0` degenerate |
| Regression impact addressed | PASS | R1-R3 + preserve list |
| Success criteria measurable | **FAIL** | SC-002 RSS unmeasurable (MAJ-009); SC-004 "≤10 s" fine |

---

## Test Coverage Assessment

| Gap | Affected | Notes |
|---|---|---|
| Cookie/context survival across reload (mechanism) | US-3, FR-003/004, R1 | Tests will silently assert wrong behavior until CRIT-003 + MAJ-006 fixes land; R1 must also assert "tabs may be closed, login persists after re-navigate" |
| Coordinator concurrency (launch + budget reservation) | FR-001, FR-006 | No concurrent-`Register`-during-launch test; no atomic budget-reservation race test beyond the (currently unimplementable) dataset row — MAJ-007 |
| SC-002 RSS read path | SC-002 | MAJ-009 |
| `max_total_tabs=0` edge | FR-006 | Is every open denied? Is 0 a valid config? |
| 0 browser-using agents | FR-001 | Does the coordinator lazy-launch on first Register or boot-launch? (Unasked Q8 from round 1, still open.) |

---

## STRIDE Threat Summary (delta from round 1)

| Component | Change in round 2 | Notes |
|---|---|---|
| BrowserCoordinator context-owner role | **new risk** | If the coordinator owns all browser contexts, a coordinator bug or a compromise of the coordinator's chromedp connection can dispose/reassign any agent's context. Mitigation: the `agentID → browserCtxID` map is the trust boundary; never expose another agent's `browserCtxID` to a manager (round-1 STRIDE note on cross-agent context IDs still applies — enforce at the coordinator). |
| Coordinator mutex / budget counter | new DoS surface | A buggy `ReserveTab` that never decrements on failure permanently shrinks the budget (agent self-DoS). Ensure failure-path `ReleaseTab`. |
| All other components | unchanged | round-1 STRIDE table stands. |

---

## Unasked Questions (still open / new)

1. **Who creates and owns each browser context — manager or coordinator?** (CRIT-003) — The spec says "manager" (Stream B:94), which is the unimplementable choice. Must be the coordinator.
2. **What is the coordinator's locking discipline across a blocking launch?** (MAJ-007) — Does `Register` hold the mutex during Chrome launch (the ADR-038 anti-pattern) or release-and-wait?
3. **How is the tab-budget check made atomic across the coordinator↔manager boundary?** (MAJ-007) — Reservation counter, or accept that two agents can both win at the boundary (weakening the dataset)?
4. **How is SC-002's RSS actually measured?** (MAJ-009) — Parent+descendant `VmRSS` sum? cgroup `memory.current`?
5. **Does the coordinator lazy-launch on first Register or boot-launch?** (carried Q8) — 0-agent dataset still missing.
6. **Re-confirm: is cookie-loss-on-crash acceptable for the "often simultaneous scraping" profile?** (OBS-004)

---

## Verdict Rationale

**FAIL (BLOCK).** One CRITICAL.

The spec's structural discipline is strong and most round-1 findings were folded in correctly. But the **headline** round-1 fix (CRIT-002 — browsing state survives Settings reload) is **defeated by chromedp's documented lifecycle**: a `WithNewBrowserContext` context is auto-disposed when its owning Go context ends, and the spec assigns that owning role to the per-agent manager whose context is canceled on every reload. The fix is recoverable — the re-adoption primitive (`WithExistingBrowserContext`, non-owning) exists and the coordinator demonstrably survives reload — but the spec must **mandate the ownership topology** (coordinator creates+owns; managers re-adopt) before any implementation begins, or the fan-out will ship the exact silent-login-loss incident round 1 was convened to prevent.

The MAJORs (MAJ-006 through MAJ-010) are spec-text fixes; MAJ-007 (coordinator locking + atomic budget reservation) and MAJ-009 (RSS measurement) are the ones that will otherwise cause late-stage implementation thrash. None is architectural beyond CRIT-003.

This is the FINAL round. The spec is NOT released to the fan-out build until CRIT-003 is resolved.

### Recommended next actions
- [ ] **CRIT-003**: Rewrite §3 interface contract + Stream A/B to mandate coordinator-creates-and-owns; managers re-adopt via `WithExistingBrowserContext`. Add the "no `WithNewBrowserContext` on a `*BrowserManager`" invariant. Resolve MIN-007 (placement) as a decision.
- [ ] **MAJ-006**: Correct "tabs preserved" → "login/localStorage preserved; tabs reopen (still authenticated)" in US-3/AC1 + hot-reload BDD.
- [ ] **MAJ-007**: Specify coordinator lock topology (no mutex across launch) + atomic budget reservation (`ReserveTab`/`ReleaseTab`); add a concurrency BDD.
- [ ] **MAJ-008**: Fix the §10 crash dataset row ("cookies intact" → "fresh empty context").
- [ ] **MAJ-009**: Specify the SC-002 RSS measurement path (parent+descendant `VmRSS`).
- [ ] **MAJ-010**: State that Stream D's unit test mocks the tab counter.
- [ ] **MIN-005/006/008**: US-8 for FR-007; BDDs for FR-008/FR-010; reconcile the `Close()` path with `coordinator.Shutdown()`.
- [ ] **OBS-004**: Re-ask the operator on crash cookie-loss for the scraping profile.
