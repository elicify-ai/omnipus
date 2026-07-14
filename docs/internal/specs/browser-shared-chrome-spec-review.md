# Adversarial Review: Browser shared Chrome + per-agent contexts (ADR-043 spec)

**Spec reviewed**: `docs/internal/specs/browser-shared-chrome-spec.md`
**Review date**: 2026-07-14
**Review round**: 1 of 2
**Verdict**: REVISE

## Executive Summary

The spec is well-structured and demonstrates strong traceability discipline, but
two CRITICAL feasibility gaps will cause production incidents if shipped as-is:
(1) crash-recovery "cookies intact" is likely infeasible because CDP browser
contexts are ephemeral — they do not survive a Chrome process restart — and no
persistence mechanism is specified; (2) hot-reload disposes every agent's
browser context on every Settings save, silently destroying all login state
while the spec claims browsing is "uninterrupted." Additionally, the Stream D
tab-budget enforcement has a hidden cross-stream dependency on Stream B (the
coordinator cannot count tabs it doesn't own), and the D2 isolation spike has
no failure fallback — gating the entire per-agent isolation story on one
integration test with no Plan B.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 5 |
| MINOR | 4 |
| OBSERVATION | 3 |
| **Total** | **14** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Crash recovery "cookies intact" is likely infeasible — CDP browser contexts are ephemeral

- **Lens**: Infeasibility (FEA-01) + Incorrectness (COR-05)
- **Affected section**: US-4/AC1 (§7), BDD "Chrome crash recovers within bound" (§8), FR-005 (§9), SC-004 (§11)
- **Description**: The spec asserts that after a Chrome crash + coordinator relaunch, "each agent's cookies are intact" (US-4/AC1) and "each agent's cookies are intact" (BDD). But CDP browser contexts created via `Target.createBrowserContext` are **in-memory constructs in the Chrome process** — they do NOT persist to disk and are destroyed when the Chrome process exits. On relaunch, the coordinator creates a fresh Chrome with NO browser contexts. Each manager must call `WithNewBrowserContext()` again, producing a **brand-new empty context** with no cookies, no localStorage, no indexedDB. The spec specifies no cookie persistence/restore mechanism (no `Storage.setStorageTracking` / `Network.setCookie` replay, no disk-backed context restore). Verified: `manager.go:1094` `bootstrapBrowserCtx` calls `chromedp.NewContext(allocCtx)` with no persistence hook; the `PersistSession` config field (config.go:2990) persists the Chrome *profile* but per-agent **non-default** browser contexts are not part of the default profile's disk persistence — they are ephemeral by CDP design.
- **Impact**: An operator relying on crash recovery (US-4 is P0) discovers that after any Chrome crash, every agent loses all login state. Agents mid-workflow (logged into GitHub, Jira, a banking portal) are silently logged out and must re-authenticate — possibly hitting 2FA, rate limits, or CAPTCHA barriers they cannot pass headlessly. This is a 3 AM incident: "Chrome crashed, all agents lost their logins."
- **Recommendation**: Pick one of three fixes and spec it explicitly:
  1. **Weaken the claim**: change US-4/AC1 and the BDD to "each agent gets a fresh empty context" (cookies NOT intact). Honest but degrades the UX promise.
  2. **Spec a cookie-restore mechanism**: before crash (or on-dispose), snapshot each context's cookies via `Network.getCookies` + localStorage via `DOMStorage` API; after relaunch, replay them via `Network.setCookie` into the new context. This is real work — add it as a D-level decision or an explicit §6 integration boundary.
  3. **Use `disposeOnDetach: false` + persist via Chrome's `--user-data-dir` partition**: investigate whether CDP's `createBrowserContext` with `compressTransport` or a per-context user-data-dir survives a restart. If chromedp 0.15.1 supports a persistent context variant, spike THAT alongside the O1 spike. If not, document the limitation.

---

#### [CRIT-002] Hot-reload silently destroys all agents' browsing sessions (cookies/login lost on every Settings save)

- **Lens**: Inconsistency (CON-06) + Incompleteness (INC-04)
- **Affected section**: §4 behavioral contract ("browsing continues uninterrupted"), US-3/AC1 (§7), D4 invariant 2 (ADR-043), BDD "hot-reload does not kill Chrome" (§8)
- **Description**: The spec's §4 says: "When the operator saves any Settings while agents are browsing, browsing continues uninterrupted (the shared Chrome is not killed)." US-3/AC1 says "both agents keep browsing." But D4 invariant 2 (ADR-043) specifies that `BrowserManager.Shutdown()` "disposes its browser context." The reload path (`loop.go:1712-1713`) calls `prior.Shutdown()` → now `coordinator.Release(agentID)`. The spec says Release is "a refcount decrement, not a process kill" — true, the Chrome PROCESS survives. But the spec does NOT mention that `Shutdown()` **also disposes the browser context** (`Target.disposeBrowserContext`), destroying that agent's cookies, localStorage, and login state. The new manager (created by `registerSharedTools` on reload) must create a **fresh** browser context. So the Chrome process is alive, but every agent's browsing session is wiped. The spec conflates "Chrome process survives" with "browsing state survives" — these are fundamentally different things.
- **Impact**: An operator saves a trivial display Setting while 3 agents are logged into sites doing research. All 3 silently lose their logins. Their next `browser_navigate` lands on a login page, not the page they were on. The agents have no idea why they're suddenly logged out and may fail their tasks. The spec's BDD ("both agents' next browser tool succeeds without relaunch") is technically true (no relaunch) but deeply misleading (the session is gone). This is the exact class of silent data loss the grill exists to catch.
- **Recommendation**: Either:
  1. **Do NOT dispose the context on reload.** Change D4 invariant 2: `Shutdown()` drops the connection only, NOT the browser context. The browser context persists in the Chrome process across reloads (CDP contexts survive as long as the Chrome process lives, regardless of which Go manager holds them — they're keyed by `browserContextId`, not by the Go `*BrowserManager` reference). The new manager must **re-adopt** the existing context (by `browserContextId`) rather than create a new one. This requires the coordinator to track `agentID → browserContextId` and hand it to the new manager on reconnect. Spec this explicitly.
  2. **OR weaken the claim honestly**: change §4 to "the Chrome process is not killed (agents relaunch their browsing sessions with fresh contexts)" and add an AC that acknowledges cookie loss on reload. This is worse UX but honest.

  Option 1 is strongly recommended — CDP contexts ARE persistent across client reconnects as long as the Chrome process lives. The fix is to NOT dispose on Shutdown, and to re-adopt by context ID on reconnect.

---

### MAJOR Findings

#### [MAJ-001] Stream D (TotalOpenTabs) has a hidden cross-stream dependency on Stream B — not parallelizable as claimed

- **Lens**: Inconsistency (CON-03) + Infeasibility (FEA-05)
- **Affected section**: §3 interface contract (`TotalOpenTabs() int`), §3 Stream D ("Depends on: Stream A + Stream B"), §3 "Parallelization note" ("B/C/D fan out in parallel")
- **Description**: The coordinator's `TotalOpenTabs()` (§3 interface contract) must sum open tabs across all agents' browser contexts. But tab counts live inside each `BrowserManager`'s `sessions` map, accessible only via the **unexported, mutex-requiring** `totalTabCountLocked()` (`manager.go:1208`, requires `m.mu` held). The coordinator owns the Chrome process + connections — it does NOT own the managers or their tab models. To implement `TotalOpenTabs()`, the coordinator needs either: (a) a new exported tab-count method on `BrowserManager` (owned by Stream B's file — `manager.go`), or (b) a push-registration mechanism where managers report tab counts to the coordinator (new code in `manager.go`, owned by Stream B). The spec claims "B/C/D fan out in parallel (different files: `coordinator.go` vs `manager.go`...)" — but D's core enforcement method (`TotalOpenTabs`) cannot be implemented without modifying Stream B's `manager.go`. The file ownership IS non-overlapping, but the **semantic dependency** means D cannot proceed independently of B.
- **Impact**: A dev agent assigned Stream D will block on Stream B: it cannot implement or test `TotalOpenTabs()` / `OpenTab` budget enforcement without B's per-context tab model. The parallel fan-out claim is false for D. If D proceeds with a stub, integration will reveal the gap late.
- **Recommendation**: Add to the interface contract (§3) an explicit tab-count reporting mechanism, defined in Stream A's first commit so both B and D code against it:
  ```go
  // On the BrowserManager (gains this in Stream B):
  func (m *BrowserManager) TotalTabs() int  // exported, locks internally
  // On the coordinator (Stream A defines, Stream D implements enforcement):
  func (c *BrowserCoordinator) RegisterManager(agentID string, mgr TabCounter)
  ```
  Or simpler: the coordinator holds `managers map[string]*BrowserManager` (same package — `pkg/tools/browser`), and `TotalOpenTabs()` iterates + sums. Either way, **call out in the parallelization note that D depends on B's exported tab-count method, and D's `TotalOpenTabs` implementation is serialized after B lands its tab model.**

---

#### [MAJ-002] `GetOrConnect(ctx)` interface lacks agentID — per-agent refcount model is impossible

- **Lens**: Ambiguity (AMB-04) + Inconsistency (CON-02)
- **Affected section**: §3 interface contract (`GetOrConnect(ctx) (cdpURL, err)` vs `Release(agentID) int`)
- **Description**: The interface contract defines `GetOrConnect(ctx context.Context) (cdpURL string, err error)` — **no agentID parameter**. But `Release(agentID string) int` requires per-agent tracking. D4 says "Refcount unit = registered browser-using manager" — but there is no registration method in the interface. A manager calls `GetOrConnect(ctx)`, gets a URL, and... the coordinator has no idea which agent it was. When that manager is later Shutdown'd on reload, `Release(agentID)` is called — but the coordinator never recorded this agentID against a GetOrConnect call. The refcount bookkeeping has no increment side tied to the agent.
- **Impact**: Either the coordinator cannot track per-agent refcounts (the D4 invariant is unimplementable as specified), or the implementer will silently add agentID to GetOrConnect — an undocumented interface change from the spec. Both are defects.
- **Recommendation**: Fix the interface contract explicitly:
  ```go
  func (c *BrowserCoordinator) GetOrConnect(ctx context.Context, agentID string) (cdpURL string, err error)
  ```
  Or add a separate `Register(agentID string)` / `Unregister(agentID string)` pair and document which call increments/decrements the refcount. The spec must make the refcount increment/decrement symmetry explicit.

---

#### [MAJ-003] D2 spike has no failure fallback — the entire isolation story gates on one test with no Plan B

- **Lens**: Incompleteness (INC-01) + Infeasibility (FEA-03)
- **Affected section**: §3 Stream B ("TDD test #1 — the spike — gates the whole stream"), §12 ambiguity table ("Resolve by the D2 spike"), BDD "window.open stays in opener's context"
- **Description**: The spec designates `TestBrowserContext_WindowOpenIsolation` as "THE D2 spike" that "gates Stream B" and is TDD test #1 (ordered first). The ADR's entire D2 decision — per-agent cookie isolation via browser contexts — depends on the O1 property (window.open from agent A's tab creates the target in A's context). If this property does NOT hold (e.g., CDP defaults new targets to the default context, not the opener's context; or chromedp 0.15.1's `WithNewBrowserContext` doesn't propagate to child target creation), the per-agent isolation model collapses. The spec specifies NO fallback: no degraded mode, no alternative isolation mechanism, no decision tree for "spike fails." §12's ambiguity entry ("Resolve by the D2 spike") resolves only whether `adoptTarget` works on a child context — not what happens if the spike fails entirely.
- **Impact**: If the spike fails, Stream B (and thus the core D2 isolation promise) has nowhere to go. The team either ships without isolation (silently violating US-2, a P0 requirement) or blocks indefinitely. A spike that gates a P0 feature must have a pre-committed fallback or an explicit "stop and re-ADR" decision point.
- **Recommendation**: Add a "D2 spike failure path" to §3 Stream B:
  > **If the O1 property does NOT hold:** the coordinator falls back to per-agent tab-set isolation WITHOUT cookie isolation (agents share the default browser context but maintain separate tab-sets via ADR-041's sessionID model). This is explicitly degraded from US-2's cookie-isolation promise and requires either (a) operator acceptance of shared cookies (the ADR's original "provisionally accepted" trade-off), or (b) an immediate re-ADR to Option B (per-agent Chromes). The spike failure is a **stop-work event** — do NOT silently ship shared cookies.

---

#### [MAJ-004] SC-002 "documented cap" is undocumented — 5-agent stress test could OOM a low-spec devpod

- **Lens**: Infeasibility (FEA-02, FEA-04) + Ambiguity (AMB-03)
- **Affected section**: SC-002 (§11), §10 TDD plan test #10 (`TestFiveAgents_ConcurrentStress`), ADR-043 D6/D7, grill O4
- **Description**: SC-002 says "RSS under a documented cap" — but **no cap value appears anywhere in the spec**. The word "documented" is a placeholder for a number that was never filled in. With the default `MaxTabs=5` per agent (config.go:2989) and `max_total_tabs=30` (D7), 5 agents could open up to 25 tabs. The ADR's D6 measures a 91 MB baseline + ~80 MB/tab blended average (~300 MB/tab for heavy sites). At heavy sites: 91 MB + 25 × 300 MB ≈ **7.6 GB**. This devpod has been observed at **3.8 GB RAM** (grill O4, acknowledged in the ADR). The 5-agent stress test with realistic page weights will OOM-kill the pod on the low end. The spec's test dataset says "5 agents, 1 tab each" (lightweight) but SC-002 says "mixed navigation" (heavier). These are inconsistent scenarios.
- **Impact**: `TestFiveAgents_ConcurrentStress` either (a) uses only `about:blank` (passing but proving nothing about real-world RAM), or (b) uses real sites and OOMs on a 3.8 GB pod (failing for the wrong reason). Either way the headline SC-002 is unmeasurable as specified.
- **Recommendation**: Specify concretely:
  1. State the RSS cap value (e.g., "≤ 2.5 GB total browser RSS").
  2. Constrain the stress test: "5 agents, 1 tab each, navigating to lightweight pages (example.com, example.org) — NOT heavy sites."
  3. Specify the minimum pod spec: "SC-002 requires ≥ 4 GB RAM; on pods with < 4 GB, run the 2-agent variant (SC-001) only and record SC-002 as deferred to the ci-omnipus 16 GB worker."
  4. Reconcile the test dataset ("1 tab each") with SC-002's "mixed navigation" — are agents opening multiple tabs or not?

---

#### [MAJ-005] R1–R3 regression tests lack concrete assertion mechanisms — not implementable as specified

- **Lens**: Infeasibility (FEA-04) + Ambiguity (AMB-05)
- **Affected section**: §10 TDD plan tests #5–#7 (R1/R2/R3), §10 test datasets ("Chrome PID unchanged"), BDD "hot-reload does not kill Chrome" ("PID P is still alive after reload")
- **Description**: The R1–R3 tests are named but their assertion mechanisms are unspecified:
  - **R1** (`TestReloadProviderAndConfig_DoesNotKillChrome`): must verify "Chrome PID unchanged" across a `ReloadProviderAndConfig` call. But how does the test obtain the PID? The spec defines an ownership marker (`$OMNIPUS_HOME/browser/shared-chrome.pid`) but the TDD plan never references it as the assertion path. Does the test read the marker? Call `coordinator.PID()`? Scan `/proc`? Each gives different guarantees.
  - **R3** (`TestClose_IsOnlyProcessKill`): must verify that **nothing other than `Close()`** kills the process. This is a negative assertion — how? Assert the coordinator's `allocCancel` is only invoked from `Close()`? Run N reload cycles and check the PID survives all of them? The spec gives no strategy.
  - **R2** (`TestCoordinator_CrashRecovery`): must verify "recovery ≤10s" and "cookies intact" (see CRIT-001). The timing assertion needs a specified measurement methodology (wall-clock from process-kill to first-successful-navigate?).
- **Impact**: Three dev agents assigned these tests will each invent a different assertion strategy, producing tests of varying rigor. R3 (the negative assertion) is particularly prone to a weak implementation that passes without actually guarding the invariant.
- **Recommendation**: Specify each test's assertion mechanism:
  - R1: "Read `coordinator.PID()` before and after `ReloadProviderAndConfig`; assert equal. Verify via `os.FindProcess(PID).Signal(syscall.Signal(0))` that the process is alive."
  - R3: "Run 5 reload cycles; after each, assert the PID is unchanged AND alive. Then call `Close()`; assert the PID is no longer alive. This proves Close() is the ONLY kill path."
  - R2: "Record wall-clock from `process.Kill()` to the first `browser_navigate` that returns nil error; assert ≤ 10s."

---

### MINOR Findings

#### [MIN-001] FR-007 (foreign-Chrome rejection) is a traceability orphan — no user story

- **Lens**: Inconsistency (CON-02)
- **Affected section**: §9 traceability matrix (FR-007 row: US column = "—")
- **Description**: FR-007 ("no foreign-Chrome adoption") has no user story. Every FR should trace bidirectionally to a US. FR-007 is a security property (grill M2) — it should have at least an operational US ("As an operator, I want the gateway to reject a foreign Chrome on 9223 so it never drives the wrong browser").
- **Recommendation**: Add US-8 (P1) for FR-007, or explicitly mark FR-007 as a non-functional security requirement with no US (and note the deviation from the structural rule).

---

#### [MIN-002] FR-008, FR-009, FR-010 have no BDD scenarios

- **Lens**: Inconsistency (CON-02)
- **Affected section**: §9 traceability matrix (FR-008 BDD = "(R3)" which is a test; FR-009 BDD = "—"; FR-010 BDD = "—")
- **Description**: Three FRs lack BDD scenarios. FR-008's BDD column shows "(R3)" — that's a test name, not a Given/When/Then scenario. FR-009 (no wire change) and FR-010 (live-view per-agent) show "—". The structural check requires every FR to have a BDD.
- **Recommendation**: Add BDD scenarios for:
  - FR-008: "Given the shared Chrome PID is P, When 3 reload cycles run AND two agents call Shutdown(), Then PID P is alive; When Close() runs, Then PID P is dead."
  - FR-010: "Given agents Jim and Ray are browsing, When a viewer attaches with agent_id=Jim, Then the screencast shows Jim's active tab, not Ray's."

  FR-009 (verify-contracts green) is arguably a build-level check, not a behavioral BDD — if so, mark it explicitly as "build gate, not BDD-eligible" and justify the deviation.

---

#### [MIN-003] Foreign-Chrome marker adoption is underspecified for PID recycling

- **Lens**: Insecurity (SEC-01) + Infeasibility (FEA-01)
- **Affected section**: §3 Stream A "Ownership marker (grill M2)", BDD "foreign Chrome on 9223 is not adopted"
- **Description**: The marker stores "pid + chrome-for-testing version." On cold start, the coordinator "verifies a holder is our Chrome before adopting." But Linux PIDs are recycled. If the old Chrome died and its PID was reused by an unrelated process (e.g., a shell), checking "is PID X alive?" returns true for the wrong process. The spec doesn't specify the verification logic: does it read `/proc/PID/cmdline`? Does it hit the CDP `/json/version` endpoint and compare the version string? Does it check the port is listening AND the version matches? Each approach has different spoofing resistance.
- **Recommendation**: Specify the adoption check: "Read the marker's PID. Verify: (1) PID is alive (`os.FindProcess` + `Signal(0)`); (2) `/proc/PID/cmdline` (Linux) / process name contains 'chrome'; (3) the CDP `/json/version` HTTP endpoint on 9223 returns a `Browser` version string matching the marker's recorded version. If any check fails, treat as foreign — reject (do not adopt)."

---

#### [MIN-004] Concurrent GetOrConnect launch-failure fan-out is unspecified

- **Lens**: Incompleteness (INC-01, INC-07)
- **Affected section**: §3 interface contract (`GetOrConnect` — "Errors if launch fails"), §3 Stream A crash detection
- **Description**: If agent A triggers `GetOrConnect` and the Chrome launch fails (binary not found, port held by a foreign process, OOM), agents B/C/D concurrently blocked on `GetOrConnect` need to receive the same error promptly — not hang waiting for a launch that already failed. The spec says GetOrConnect "Errors if launch fails" but doesn't specify whether the coordinator broadcasts the error to all waiters, or whether each waiter independently retries/discovers. There's also no timeout specified for how long a connector waits before declaring the launch failed.
- **Recommendation**: Specify: "The coordinator's GetOrConnect blocks all concurrent callers on a single launch attempt. If the launch fails, ALL waiters receive the error immediately (broadcast). If the launch succeeds, all waiters receive the cdpURL. There is no per-caller retry — a failed launch surfaces to all agents, who then fail their tool calls with the coordinator's error message."

---

### Observations

#### [OBS-001] SPA BrowserLivePanel already keys on agentId — Stream C's "agent identity chip" is smaller than implied

- **Lens**: Overcomplexity (CPX-07)
- **Affected section**: §3 Stream C ("SPA BrowserLivePanel multi-agent UX — the header/chip")
- **Suggestion**: `src/components/browser/BrowserLivePanel.tsx:94` already keys its mount on `` `${browserPanel.sessionId}:${browserPanel.agentId}` `` and passes `agentId` to `BrowserLiveView`. The agent identity is already available in the view's props. Stream C's "show which agent's context is driven — the header/chip" is a rendering/display change (add a label to the existing header), not a data-model or binding change. The spec could note this to right-size Stream C's scope estimate.

---

#### [OBS-002] verify-contracts will show a generated-file diff from the BrowserAttachFrame.yaml description change

- **Lens**: Incorrectness (COR-05)
- **Affected section**: FR-009 (§9), §3 Stream C ("make verify-contracts must stay green")
- **Suggestion**: The spec correctly claims no wire-schema *field* change. But `make verify-contracts` runs `git diff --exit-code -- contracts/ pkg/api/generated/ src/lib/api/generated/` — a description-prose change in `BrowserAttachFrame.yaml` will regenerate the Go/TS types (updated doc comments) and produce a diff. This is fine IF `scripts/gen-contracts.sh` is re-run and the diff committed per Constraint #8's 5-step process. The spec should explicitly note: "Run `make gen-contracts`, commit the generated diff alongside the YAML change, THEN verify-contracts is green." Otherwise a dev agent who changes only the YAML will see verify-contracts fail on uncommitted drift and be confused.

---

#### [OBS-003] `cdp_url` external-Chrome mode interaction with global tab budget is unspecified

- **Lens**: Incompleteness (INC-04)
- **Affected section**: §6 integration boundaries ("existing cdp_url still forces a fully-external Chrome, coordinator bypassed"), D7 (§11)
- **Suggestion**: When `cdp_url` is set, the coordinator is "bypassed." But does `max_total_tabs` (D7) still apply? If the coordinator is bypassed, who enforces the global budget? The spec should clarify: either (a) `cdp_url` mode is exempt from the global budget (operator's external Chrome, their problem), or (b) the budget is enforced at the `OpenTab` tool level regardless of coordinator involvement. Currently the spec ties enforcement to "coordinator TotalOpenTabs + OpenTab enforcement" — which a bypassed coordinator cannot do.

---

## Structural Integrity

### Plan-Spec Format Checklist

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1 through US-7 all have ≥1 AC |
| Every acceptance scenario has BDD scenarios | **FAIL** | US-7/AC1 (no wire change) has no BDD — BDD column is "—" |
| Every BDD scenario has `Traces to:` reference | PASS | All 6 BDD scenarios in §8 have explicit "Traces to" |
| Every BDD scenario has a test in TDD plan | PASS | All 6 map to tests #1–#9 in §10 |
| Every FR appears in traceability matrix | PASS | FR-001 through FR-010 all present in §9 |
| Every BDD scenario in traceability matrix | PASS | All 6 BDD scenario names appear in the BDD column |
| Test datasets cover boundaries/edges/errors | **PARTIAL** | Covers happy (1/5 agents), budget boundary (max=2), error (crash), foreign Chrome. Missing: concurrent browser-tool + live-view on the SAME agent simultaneously; empty context (0 tabs); max_total_tabs=0 edge; reload-while-tabs-at-budget edge |
| Regression impact addressed | PASS | §10 "Regression requirements" lists R1–R3 + preserve list explicitly |
| Success criteria are measurable | **FAIL** | SC-002's "documented cap" is never documented — no RSS threshold value appears (see MAJ-004) |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|-----------------|-------------------|
| Cookie persistence across crash | No test verifies whether cookies survive a Chrome restart (CRIT-001). If the claim is kept, a test must assert it; if dropped, the BDD must change. | US-4, FR-005, R2 |
| Context disposal on reload | No test verifies whether agents' browser contexts (cookies/login) survive a reload (CRIT-002). The spec must decide: re-adopt by context ID, or accept cookie loss. | US-3, FR-003/FR-004, R1 |
| Concurrent same-agent access | No test for a browser tool call and a live-view screencast operating on the SAME agent's context simultaneously (the ADR-038 no-lock-across-CDP invariant under the new context model). | All streams |
| Budget edge: max_total_tabs=0 | No test for the degenerate "0 tabs allowed" config (should every OpenTab be denied? Is 0 even valid?). | FR-006, D7 |
| Budget race | No test for two agents calling OpenTab simultaneously when only 1 slot remains under the global budget. | FR-006, D7 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Agent count | Single boundary (1, 5). Missing: 0 agents (no Chrome launched — does the coordinator lazy-init or boot-launch?), exactly max_total_tabs agents each at 1 tab. | Add 0-agent and boundary-agent datasets |
| Tab weight | "Mixed navigation" is vague. No dataset specifies page weight (light vs heavy) for the stress test. | Specify: SC-002 uses example.com (light); the 10-agent ci-omnipus gate uses mixed real sites (heavy). |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| BrowserCoordinator (launch authority) | risk | ok | ok | risk | risk | ok | **Spoofing**: foreign Chrome on 9223 — mitigated by marker but PID-recycling gap (MIN-003). **Info Disclosure**: CDP endpoint has no auth — any local process can connect to 9223 and drive Chrome (pre-existing, not new, but N agents' contexts are now exposed). **DoS**: a single agent opening max_total_tabs blocks all other agents' tab creation. |
| Browser contexts (per-agent) | ok | risk | ok | risk | ok | ok | **Tampering**: can agent A dispose agent B's context? Only if it knows B's browserContextId — spec must verify the coordinator never leaks context IDs across agents. **Info Disclosure**: CDP contexts isolate cookies but NOT renderer processes — a renderer compromise leaks across agents (acknowledged in ADR, out of scope). |
| Ownership marker file | risk | risk | ok | ok | ok | ok | **Spoofing/Tampering**: marker is a plain file (`shared-chrome.pid`) — a malicious local process could write a fake marker to trick the coordinator into adopting its Chrome. No integrity check (HMAC, owner-only permissions specified). Pre-existing threat level (local trust boundary), but now the marker controls a shared resource. |

---

## Unasked Questions

1. **What happens to agents' browser contexts on reload?** (CRIT-002) — The spec says Chrome isn't killed, but does `Shutdown()` dispose the context? If so, agents lose all login state on every Settings save. If not, how does the new manager re-adopt the old context by ID?
2. **How are cookies restored after a Chrome crash?** (CRIT-001) — CDP browser contexts are ephemeral. Is there a persistence mechanism, or is "cookies intact" aspirational?
3. **If the D2 spike fails (O1 property doesn't hold), what ships?** (MAJ-003) — Is it shared cookies (degraded), per-agent Chromes (Option B), or a release blocker?
4. **How does the coordinator's `TotalOpenTabs()` obtain per-manager tab counts?** (MAJ-001) — The coordinator doesn't own the managers. What's the data flow?
5. **What is the RSS cap for SC-002?** (MAJ-004) — "Documented cap" has no number. What pod spec is required?
6. **Does `cdp_url` external-Chrome mode enforce `max_total_tabs`?** (OBS-003) — If the coordinator is bypassed, who enforces the budget?
7. **Can the ownership marker be spoofed by a local process?** (MIN-003, STRIDE) — No integrity mechanism specified.
8. **What happens when 0 browser-using agents are registered?** — Does the coordinator launch Chrome eagerly at boot, or lazily on first GetOrConnect?

---

## Verdict Rationale

**REVISE.** The spec has strong structural discipline (bidirectional traceability, explicit non-behaviors, TDD ordering, grill-finding cross-references) and the ADR-043 decision direction is sound. However, two CRITICAL findings must be resolved before implementation:

- **CRIT-001** (ephemeral contexts vs "cookies intact" after crash) strikes at the core of US-4/P0. The crash-recovery promise is likely unimplementable as written without a persistence mechanism the spec doesn't mention. This must either be spec'd or honestly weakened.
- **CRIT-002** (reload disposes contexts, silently destroying login state) is the more insidious defect: the spec's §4 "browsing continues uninterrupted" is true at the process level but false at the session level. An operator will lose logins on every Settings save. The fix (re-adopt context by ID, do NOT dispose on Shutdown) is architecturally clean but must be spec'd explicitly.

The MAJOR findings (MAJ-001 through MAJ-005) are implementation-blocking for the parallel fan-out: the interface contract is underspecified for parallelization (MAJ-001, MAJ-002), the spike lacks a fallback (MAJ-003), the headline success criterion is unmeasurable (MAJ-004), and the regression tests can't be implemented without further design (MAJ-005). These should be resolved in this revision pass — they are all spec-text fixes, not architectural changes.

### Recommended Next Actions

- [ ] **CRIT-001**: Decide crash-recovery cookie fate (weaken claim vs spec persistence vs spike persistent contexts). Rewrite US-4/AC1 + BDD to match.
- [ ] **CRIT-002**: Decide reload-context fate (re-adopt by context ID recommended). Update D4 invariant 2 + §4 + US-3 + BDD.
- [ ] **MAJ-001**: Add tab-count reporting to the interface contract; correct the parallelization note for D's B-dependency.
- [ ] **MAJ-002**: Add agentID to GetOrConnect (or add Register/Unregister); make refcount symmetry explicit.
- [ ] **MAJ-003**: Write the D2 spike failure path (degraded mode + stop-work event).
- [ ] **MAJ-004**: Fill in the SC-002 RSS cap value, pod spec, and page-weight constraints.
- [ ] **MAJ-005**: Specify R1–R3 assertion mechanisms (PID source, negative-assertion strategy, timing methodology).
- [ ] **MIN-001/002**: Add US-8 for FR-007; add BDDs for FR-008/FR-010 (or justify deviations).
- [ ] **MIN-003/004**: Specify marker verification logic; specify launch-failure broadcast.
