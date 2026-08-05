# ADR-043 — Browser: one shared Chrome + per-agent browser contexts (tab-sets)

- **Status:** Accepted (decision recorded 2026-07-14; implementation gated behind this ADR, ~2 sprints on `bugfixes2`, then UAT)
- **Deciders:** Daniel Piatkowski (operator), lead engineering
- **Extends/amends:** [[ADR-038]] (live interactive browser panel — its D1↔D5 amendment made `browser_attach.session_id` "correlation only" against one hardcoded `"default"` tab; this ADR re-introduces session identity as *per-agent*), [[ADR-040]] (take-the-wheel — preserved within an agent's context), [[ADR-041]] (browser tabs / multi-target — the tab-set model this builds on, one tab-set per agent), [[ADR-042]] (browser provisioning — unchanged; one managed Chrome still downloads at boot).

## Motivation

[FACT] `pkg/agent/loop.go:185` creates one `*browser.BrowserManager` per registered agent (`browserMgrs`). [FACT] Each manager's managed-mode `ensureStarted` builds its **own** `chromedp.NewExecAllocator` with `chromedp.Flag("remote-debugging-port", browserDebugPort)` where `browserDebugPort == "9223"` (`pkg/tools/browser/manager.go:40,564`). [FACT] The port is pinned because the sandbox allow-lists are a fixed compiled set: `pkg/gateway/sandbox_apply.go:388` (bind) and `:419` (connect) append `browser.DebugPort`; a dynamic per-manager port cannot be followed by Landlock/seccomp.

Consequence (investigated, 2026-07-14): **only the first browser-using agent can launch a managed Chrome; every other agent's first browser call fails** — today cleanly (`checkDebugPortAvailable` → *"port 9223 is already in use"*, `manager.go:81-83`), pre-ADR-042 as an opaque timeout. [FACT] On the seeded policy only **Jim and Ray** have browser tools (`pkg/coreagent/core.go`), so 2 of 4 core agents collide on a default install (plus any custom browser-using agent). [FACT] A latent second collision sits behind any "distinct ports" fix: all managers share `ProfileDir ~/.omnipus/browser/profiles/default/` (`manager.go:135`), so two concurrent Chromes would clobber each other's `SingletonLock`/cookies.

[FACT] **Today's actual model is already a single shared browsing surface**: per ADR-038's D1↔D5 amendment, *all* agents' browser tools operate on one hardcoded `"default"` tab (`manager.go:253,831`), and `browser_attach.session_id` is "correlation only." The hybrid therefore *expands* identity (one global tab → per-agent tab-sets), it does not reduce isolation that exists today.

Operator decision (2026-07-14): fix the concurrency limit now (not defer to v0.3). Four options were evaluated; the operator chose **C + ADR-041 hybrid** — one shared Chrome + per-agent tab-sets.

### Scale & isolation profile (operator-confirmed 2026-07-14)
[FACT — operator input] The target is up to **~10 browser-using agents** whose browsing is **often simultaneous** (parallel research / scraping / multi-site workflows — multiple agents actively loading pages at once, not just occasional lookups). [FACT — operator input] **Cookie/login isolation per agent is sufficient**; hard *process*-level isolation between agents' browsers is **not required** for v1, and one Chrome crash taking down all browsing simultaneously is an **accepted** trade-off.

This profile is load-bearing for the decision: it confirms the hybrid (one Chrome + per-agent contexts) and **rules out Option B (per-agent Chromes) and the pool variant for v1** — both trade extra Chrome-process RAM (≈ 4–5 GB and ≈ 2.2–2.5 GB respectively at 10 agents, vs ≈ 1.5–2 GB for the hybrid; rough order-of-magnitude estimates, not measured) for process isolation the operator has explicitly declined. *(RAM caveat: browser contexts isolate cookies/storage but NOT renderer processes — each tab is its own renderer (~50–300 MB, well-known range); the hybrid pays the browser-process overhead once but renderers still scale with total live tabs.)* Process-level isolation and a tunable process-count ("pool") are recorded below as a **deferred escape hatch**, not v1 scope.

## Decisions

### D1 — One gateway-scoped shared Chrome; the coordinator is the single launch authority (no probe-and-dial)
The gateway runs **one** managed Chrome on the fixed port 9223, owned by a **`BrowserCoordinator`** (new type, gateway-level). Managers **never probe port 9223 and dial it directly** — that path is insecure (a manager cannot distinguish *our* Chrome from an operator's unrelated Chrome or a stale orphan on 9223; the CDP endpoint carries no identity token — grill finding M2). Instead a manager's `ensureStarted` always **asks the coordinator** `GetOrConnect(agentID)`, which returns a `ws://127.0.0.1:9223/...` URL. The coordinator: (a) launches Chrome if none is running (it owns the `chromedp.NewExecAllocator` + `allocCancel`, **not** the manager — see D4); (b) stamps an ownership marker `$OMNIPUS_HOME/browser/shared-chrome.pid` (pid + the chrome-for-testing version string) so a cold start can detect+adopt a *prior* gateway's still-living Chrome (and a foreign/unrelated holder is rejected, not silently attached to); (c) hands every manager a remote-allocator connection. The remote-allocator path (`manager.go` `m.cfg.CDPURL != ""` branch) is reused unchanged — the coordinator just populates it automatically. **Confidence: High** (remote-allocator exists; the coordinator + marker is the net-new, well-scoped logic). **Replaces** the ADR's earlier "probe dial" wording (M2).

### D2 — Per-agent isolation via CDP browser contexts (the load-bearing decision)
Each agent's manager creates its own **CDP browser context** — a separate cookie/localStorage/indexedDB partition **within the single Chrome** — using `chromedp.WithNewBrowserContext()` (chromedp v0.15.1; verified against the module cache: `chromedp.go:492-496` + `chromedp_test.go:597 TestBrowserContext`, backed by cdproto `target.CreateBrowserContext`, `cdproto/target/target.go:198-246`). Each browser context owns that agent's tab-set (ADR-041 model). Outcome: **agents do NOT share cookies/login state across agents** — the isolation regression the operator provisionally accepted is *mitigated* by this primitive, not conceded. Within a single agent's context, the ADR-040 take-the-wheel model is preserved unchanged (human + that agent intentionally share the agent's logged-in context). **D2 spike acceptance criterion (grill O1):** a `target="_blank"`/`window.open` fired from agent A's tab must create the new target in **agent A's browser context** (CDP defaults new targets to the opener's context — correct — but this is *the* cross-agent-isolation property and must be the spike's explicit pass/fail). **Confidence: High** on the isolation primitive; **Medium** on cleanly adopting it into Omnipus's `sessions`-map/`activeIdx` bookkeeping (the spike confirms).

### D3 — No wire change; `agent_id` was already the live-view binding key
**Rewritten (grill M4):** the gateway *already* binds the live view on agent identity — `browser_ws.go:530` resolves `BrowserManagerForAgent(frame.AgentId)`, then `:544` attaches `mgr.Live().Attach(browser.DefaultSessionID, …)`. ADR-038's D1↔D5 made `session_id` "correlation only" **within one agent's single tab-set**; `agent_id` was *always* the binding key. ADR-043 does **not** reverse that. What actually changes: N managers now run concurrently (today only one can), and each manager's tab-set lives in its **own isolated browser context** (D2). `session_id` stays correlation-only; `agent_id` + `DefaultSessionID` already disambiguate. **Contract (Constraint #8): NO wire-schema change required** (confirmed against `browser_ws.go`); update only the `BrowserAttachFrame.yaml` *description* prose (the "single actual browser tab" wording → "active tab in that agent's browser context"). **Confidence: High** (binding path verified in code).

### D4 — Coordinator ownership contract (resolves C1 hot-reload, M1 crash-relaunch, M5 refcount)
The `BrowserCoordinator` owns the shared Chrome **process**; managers own only their **connection + browser context**. Hard invariants (grill C1 — the CRITICAL):

1. **The launcher's `allocCtx`/`allocCancel` live on the coordinator, not the manager.** No per-agent code path may cancel the ExecAllocator.
2. **`BrowserManager.Shutdown()` on a shared-Chrome manager drops only that manager's connection + disposes its browser context** (`Target.disposeBrowserContext`); it **never kills the Chrome process.** This removes the incident-class bug where `registerSharedTools`' per-reload `prior.Shutdown()` (`loop.go:1712-1713`) would `allocCancel`→kill Chrome on every Settings save, dropping every agent's browsing.
3. **`registerSharedTools`' `prior.Shutdown()` becomes `coordinator.Release(agentID)`** — a refcount decrement, not a process kill.
4. **The Chrome process is killed only by the coordinator, only on gateway `Close()`** (`loop.go:2792-2794` path). **Refcount unit = registered browser-using manager** (grill M5); since managers are always-on from registration to reload, the refcount is ≥ (browser-using agents) for the whole gateway lifetime — **there is no "last detaches" kill in v1** (the earlier "configurable last-detach" clause is struck: managers don't detach, so it had no trigger). Chrome lives for the gateway lifetime; killed on stop. Simple and honest.

**Crash detection + relaunch (grill M1):** the launcher manager owns detection — it holds the `os/exec` process handle and waits on it; on process exit it signals the coordinator. The coordinator relaunches Chrome and **only then** unblocks connectors. A **connector** manager's `m.started` latch must **reset on connection drop** (today `started` is set true at `manager.go:476/623` and reset only by `Shutdown()` — never on crash, so a connector would silently keep a dead `started=true` and fail forever). The connector's reconnect re-asks the coordinator, which blocks until relaunch completes, then returns a fresh connection. Bounded recovery T. The bootstrap double-launch race is serialized by the coordinator's own mutex (single launch authority — D1) with `checkDebugPortAvailable` as a backstop *diagnostic*, not a lock. All launch/connect CDP calls run with the manager mutex released (ADR-038 no-lock-across-CDP discipline). **Confidence: Medium→High** (the invariants are now specified, not hand-waved; the launcher-wait detector is standard `os/exec`).

### D5 — Sandbox unchanged (no range expansion)
One Chrome on 9223 keeps the existing fixed-port allow-list (`sandbox_apply.go:388,419`) byte-for-byte. No `9223–9230` range, no widened attack surface. This is a primary advantage of the hybrid over Option B. **Confidence: High.**

### D6 — RAM framing, corrected (renderer vs browser-process terms — grill M3)
**Measured (this host, 2026-07-14):** one `chrome-headless-shell` + 1 `about:blank` tab = **91 MB across 3 procs** (`--headless --no-sandbox --disable-gpu`). Per-tab incremental varies by site weight (~40 MB light → ~300 MB heavy; well-known renderer range). The dominant RAM term is **renderers, which scale with total live tabs** — *not* with the number of Chrome processes. One-Chrome-vs-N-Chromes saves only the **browser-process overhead** (≈ 200–400 MB × N, the parent+GPU+zygote that one Chrome pays once), not the multi-GB renderer term. So: Option B's N× cost is real but **smaller than this ADR's first draft implied** (it over-stated the saving). The hybrid is still preferred at the operator-confirmed ~10-agent/often-simultaneous profile because (a) it pays browser-process overhead once, and (b) Option B's N Chromes each still host the same total tabs. Constraint #3 (<10 MB security-feature overhead) is already exceeded by one Chrome (accepted at ADR-038); the hybrid does not worsen it. **Confidence: High** (measured baseline + standard renderer model).

### D7 — Global tab budget is a v1 decision, not an implementation note (grill M3)

> **AMENDED 2026-08-05 (operator directive, issue #592).** The global budget is
> **removed**: `tools.browser.max_total_tabs` now defaults to **unlimited**, and
> `<= 0` means unlimited rather than "fall back to 30". A positive value still
> caps, so the mechanism below is intact and opt-in. Per-agent `MaxTabs` (5) is
> unchanged and is now the only default ceiling.
>
> **Why**, and the part D7 got right: a companion change (same issue) made idle
> reaping per-tab at a 5-minute TTL swept every minute, so *abandoned* tabs no
> longer accumulate — which is what the 30 was mostly absorbing in practice.
>
> **What D7 got right and this amendment knowingly accepts:** an idle reaper
> bounds abandoned tabs, NOT tabs kept actively in use. D7's own worst case —
> "10 agents × 5 tabs = 50 live renderers... multiple GB and OOM a small host" —
> is a *concurrency* scenario the reaper cannot touch, because an in-use tab is
> touched more often than the TTL. Measured on the UAT box: ~390MB for the first
> browsing context, then 74-268MB RSS per additional renderer; two busy
> research-capable agents at their per-agent cap reach ~1.8-2.8GB, four up to
> ~5.5GB. There is no global concurrent-agent limit elsewhere in the stack, so
> the effective ceiling now scales with roster size instead of being fixed.
>
> This was accepted deliberately after the risk was put to the operator with
> that arithmetic. Operators on small hosts should set `max_total_tabs`
> explicitly; the coordinator logs the effective budget at boot so the new
> default is visible on upgrade rather than silent. Revisit if a host-RAM-derived
> default ceiling is wanted (the config already auto-detects RAM for agent
> parallelism).

[FACT] `MaxTabs` is **per-agent** (default 5, `manager.go:105`, copied at `loop.go:1645`); there is **no global cap today.** At 10 agents × 5 tabs = 50 live renderers, worst-case heavy sites can reach multiple GB and OOM a small host — this can break the "often simultaneous" promise and is therefore a **v1-scope D-level decision**, not a plan-spec detail. Decision: introduce a **global tab budget** `tools.browser.max_total_tabs` (default **30**, sized from the measured ~91 MB baseline + ~80 MB/tab blended average → ~2.5 GB headroom for browsing on a typical 8 GB+ host; tunable). The coordinator enforces it: a `browser_open_tab` that would exceed the global budget is denied with a clear error (the agent can `browser_close_tab` first). Per-agent `MaxTabs` stays as the per-tenant courtesy cap. **Confidence: Medium** (the default is a reasoned estimate from one measurement; the cap itself is the hard guarantee, the exact default is tunable). Plan-spec must spec the budget enforcement + the denial error.

## Consequences
**Positive:** all browser-using agents browse concurrently; one Chrome (browser-process overhead paid once); sandbox allow-list unchanged; cross-agent cookie/login sharing *mitigated* by browser contexts (D2); ADR-040/041 models reused; ADR-042 provisioning untouched; hot-reload (Settings save) no longer drops browsing (D4 invariant 2).

**Risks / must-handle:**
- **Coordinator correctness (D4):** the four ownership invariants (above) are the load-bearing correctness property; a violation re-introduces C1 (reload kills Chrome) or leaks the process. Tests must assert: (R1) `ReloadProviderAndConfig` mid-browsing does NOT kill the shared Chrome while siblings are attached; (R2) Chrome-crash recovery across launcher + connectors within bounded T; (R3) gateway `Close()` is the only process-kill path. Single biggest implementation risk.
- **Browser-context adoption (D2):** mapping ADR-041's sessionID-keyed tab-set onto per-agent browser contexts + re-binding the screencast to a context's active tab is the bulk of the work. The D2 spike (O1 property) gates this.
- **Shared-process blast radius:** one Chrome crash kills every agent's browsing simultaneously (today only one agent affected). **Accepted for v1** (operator-confirmed); the D4 relaunch path restores all agents within T.
- **Global tab budget (D7):** without it the "often simultaneous" profile can OOM; D7 makes it a hard v1 cap.
- **Target-event fan-out (grill O2):** `Target.setDiscoverTargets(true)` is browser-global; with N agents each `ListenTarget`-ing, every target event fans to all N. The `OpenerID ∈ tracked` filter makes this *wasteful, not incorrect*; plan-spec notes it, profiling may push a single root listener.
- **Take-the-wheel multi-agent UX (ADR-040):** the live panel must show unambiguously *which agent's* context the human drives — carry as an explicit D3 plan-spec scenario, not just a risk note.

**Accepted limitations:** process-level state (GPU cache, crash dumps) is shared across agents — cookies/localStorage/login are NOT (D2 isolates them). If an operator specifically wants agents to *share* a login (e.g., Jim logs in, Ray reuses it), that becomes a deliberate per-context opt-in (future), not the default.

## Alternatives considered (one line each)
- **A — one shared `BrowserManager`, no per-agent contexts:** loses the per-agent accessor model (ADR-038 D4) AND forces shared-everything cookies — strictly worse than D2's per-agent contexts. Rejected.
- **B — per-agent distinct ports + per-agent `ProfileDir`:** fixes concurrency with isolation preserved, but costs N Chrome processes (RAM, indefensible for self-hosted) and requires widening the sandbox allow-list to a port range (attack surface). Rejected on D6 grounds.
- **D — serialize; others get a clear "set `cdp_url`" error:** the honest stopgap the architect recommended; rejected by the operator in favor of actually fixing concurrency now. (The clear-error string from ADR-042's preflight is retained regardless — it remains the diagnostic if the coordinator itself is unavailable.)

## Gaps & ambiguities (post-grill; remaining items for `/plan-spec` / the spike)
- **[RESOLVED by D4]** Grill C1 (hot-reload kills Chrome), M1 (crash relaunch), M5 (refcount unit) — now specified as D4 ownership invariants + launcher-wait detector. Tests R1–R3 must assert them.
- **[RESOLVED by D1]** Grill M2 (launch-vs-spoof) — coordinator is the single launch authority with an ownership marker; probe-and-dial removed.
- **[RESOLVED by D3]** Grill M4 (D3 framing) — no wire change; `agent_id` was already the binding key.
- **[RESOLVED by D7]** Grill M3 (global tab budget) — promoted to a v1 decision with a default (30) sized from the measured 91 MB baseline.
- **[UNKNOWN — spike-gated]** Whether adopting browser contexts forces any change to the ADR-041 tab-adoption/screencast-re-bind paths (the `sessions` map + `activeIdx` is keyed by sessionID today). The D2 spike (O1 property: `window.open` lands in the opener's context) gates this.
- **[UNKNOWN]** Exact coordinator placement: gateway-owned type in `pkg/gateway` vs a shared singleton in `pkg/tools/browser`. Affects testability + import graph — plan-spec decides.
- **[ASSUMPTION]** Browser contexts isolate cookies/localStorage/indexedDB/cache but are NOT separate renderer processes; a renderer compromise is not isolated per agent. Out of scope (threat model is agent-isolation-from-agent, not browser-RCE).
- **[SCOPE — grill O4]** The ~10-often-simultaneous profile **cannot be validated on this devpod** (resource-constrained, seen 3.8–15 GB). The headline UAT validates **5 agents concurrent** (task #15); the full 10-simultaneous claim is validated on the **`ci-omnipus` 16 GB worker** as a stress gate, or explicitly descoped to "2–5 concurrent, more queued" for self-hosted small hosts. Plan-spec must pick one.

### Deferred escape hatch (NOT v1 — reassessed post-grill M3, still deferred)
The grill asked whether the **pool** variant should be v1 given the renderer budget. D6's corrected framing shows the hybrid still wins at the operator profile (browser-process overhead paid once; Option B's N Chromes each still host the same total tabs), and D7's global tab budget keeps one Chrome viable. So the **pool** (1→M Chromes) and **per-agent hard-isolation opt-in** remain **deferred** — re-evaluate if process-level isolation becomes a requirement or concurrent browser agents grow well beyond ~20. D1's coordinator + D2's contexts admit them later without redesign.

## Confidence (per decision)
- **D1 coordinator-as-launch-authority:** **High.** Basis: remote-allocator path exists; coordinator + ownership marker is well-scoped. (Was "probe-dial" Medium → now High after M2 fix.)
- **D2 browser contexts:** **High** (primitive verified), **Medium** (clean adoption — spike-gated).
- **D3 no wire change:** **High** (binding path verified in `browser_ws.go:530,544`). (Was Medium — M4 fix raised it.)
- **D4 coordinator ownership:** **Medium→High.** Invariants now specified (was the under-specified CRITICAL); remaining Medium on the launcher-wait detector edge cases + coordinator placement.
- **D5 sandbox unchanged:** **High.** Verified `sandbox_apply.go:388,419`.
- **D6 RAM framing:** **High.** Measured 91 MB baseline + corrected renderer-vs-browser-process model.
- **D7 global tab budget:** **Medium.** Hard cap is the guarantee; the default (30) is a reasoned estimate from one measurement — tunable.

## Next steps
1. **D2 spike** (tab-set-on-browser-context; pass/fail = the O1 `window.open` property) — gates D2 adoption confidence. **Do this before plan-spec.**
2. **`/plan-spec`** the chosen direction (BDD/TDD, traceability), decomposed for parallel-agent execution (fan-out waves): coordinator (D1/D4) ‖ contexts (D2) ‖ live-view per-agent binding (D3) ‖ global budget enforcement (D7) ‖ tests. Then implement in waves → 7-reviewer + intensive critical review → fix → stress test.
3. **Stress gate on `ci-omnipus`** (16 GB) for the 5-agent (this pod) → 10-agent (worker) concurrent profile (O4).
