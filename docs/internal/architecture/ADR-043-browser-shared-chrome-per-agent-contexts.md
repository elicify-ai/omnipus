# ADR-043 — Browser: one shared Chrome + per-agent browser contexts (tab-sets)

- **Status:** Accepted (decision recorded 2026-07-14; implementation gated behind this ADR, ~2 sprints on `bugfixes2`, then UAT)
- **Deciders:** Daniel Piatkowski (operator), lead engineering
- **Extends/amends:** [[ADR-038]] (live interactive browser panel — its D1↔D5 amendment made `browser_attach.session_id` "correlation only" against one hardcoded `"default"` tab; this ADR re-introduces session identity as *per-agent*), [[ADR-040]] (take-the-wheel — preserved within an agent's context), [[ADR-041]] (browser tabs / multi-target — the tab-set model this builds on, one tab-set per agent), [[ADR-042]] (browser provisioning — unchanged; one managed Chrome still downloads at boot).

## Motivation

[FACT] `pkg/agent/loop.go:185` creates one `*browser.BrowserManager` per registered agent (`browserMgrs`). [FACT] Each manager's managed-mode `ensureStarted` builds its **own** `chromedp.NewExecAllocator` with `chromedp.Flag("remote-debugging-port", browserDebugPort)` where `browserDebugPort == "9223"` (`pkg/tools/browser/manager.go:40,564`). [FACT] The port is pinned because the sandbox allow-lists are a fixed compiled set: `pkg/gateway/sandbox_apply.go:388` (bind) and `:419` (connect) append `browser.DebugPort`; a dynamic per-manager port cannot be followed by Landlock/seccomp.

Consequence (investigated, 2026-07-14): **only the first browser-using agent can launch a managed Chrome; every other agent's first browser call fails** — today cleanly (`checkDebugPortAvailable` → *"port 9223 is already in use"*, `manager.go:81-83`), pre-ADR-042 as an opaque timeout. [FACT] On the seeded policy only **Jim and Ray** have browser tools (`pkg/coreagent/core.go`), so 2 of 4 core agents collide on a default install (plus any custom browser-using agent). [FACT] A latent second collision sits behind any "distinct ports" fix: all managers share `ProfileDir ~/.omnipus/browser/profiles/default/` (`manager.go:135`), so two concurrent Chromes would clobber each other's `SingletonLock`/cookies.

[FACT] **Today's actual model is already a single shared browsing surface**: per ADR-038's D1↔D5 amendment, *all* agents' browser tools operate on one hardcoded `"default"` tab (`manager.go:253,831`), and `browser_attach.session_id` is "correlation only." The hybrid therefore *expands* identity (one global tab → per-agent tab-sets), it does not reduce isolation that exists today.

Operator decision (2026-07-14): fix the concurrency limit now (not defer to v0.3). Four options were evaluated; the operator chose **C + ADR-041 hybrid** — one shared Chrome + per-agent tab-sets.

## Decisions

### D1 — One gateway-scoped shared Chrome; managers connect, not launch
The gateway runs **one** managed Chrome on the fixed port 9223. The first manager to need it launches (existing `ensureStarted` managed branch + `Preprovision` from ADR-042 already owns the binary); subsequent managers **connect** to the running Chrome via `chromedp.NewRemoteAllocator(context.Background(), "ws://127.0.0.1:9223/...")` — the remote-allocator path **already exists** (`manager.go` `m.cfg.CDPURL != ""` branch) and is exactly how an operator can force the shared-Chrome shape today via `tools.browser.cdp_url`. The new work is **auto-detect**: a manager that finds 9223 already held (by the preflight, or by a probe dial) dials it instead of failing. **Confidence: High** (primitive exists; auto-detect/election is the net-new logic).

### D2 — Per-agent isolation via CDP browser contexts (the load-bearing decision)
Each agent's manager creates its own **CDP browser context** — a separate cookie/localStorage/indexedDB partition **within the single Chrome** — using `chromedp.WithNewBrowserContext()` (chromedp v0.15.1; verified against the module cache: `chromedp_test.go:597 TestBrowserContext` exercises it, backed by cdproto `target.CreateBrowserContext`, `cdproto/target/target.go:198-246`). Each browser context owns that agent's tab-set (ADR-041 model). Outcome: **agents do NOT share cookies/login state across agents** — the isolation regression the operator provisionally accepted is *mitigated* by this primitive, not conceded. Within a single agent's context, the ADR-040 take-the-wheel model is preserved unchanged (human + that agent intentionally share the agent's logged-in context). **Confidence: High** on the isolation primitive; **Medium** on cleanly adopting it into Omnipus's manager/tab-set architecture (the core implementation work — see Gaps).

### D3 — Per-agent session identity; `browser_attach` binds to the agent's context
The literal `"default"` session becomes **per-agent**: a browsing context = (agent → browser-context-id → ordered tab-set with active pointer). The gateway WS handler resolves the manager/context for the attached agent and binds the live-view screencast to **that** agent's active tab. [FACT] `browser_attach` already carries `activeAgentId` (ADR-038 D5); `session_id` today is "correlation only." This decision makes the agent identity the **binding key** for the live view (reverting the D1↔D5 "correlation only" semantics — deliberately). **Contract (Constraint #8):** the schema already carries `session_id` + `activeAgentId`; the likely change is **semantics, not schema** — but this must be confirmed in `/plan-spec` against every `browser_*` frame. If a schema delta is needed, follow the 5-step contract process. **Confidence: Medium** (semantics change is certain; whether it forces a wire-schema change is not yet confirmed).

### D4 — Chrome lifecycle + coordinator (election, refcount, shutdown)
Exactly one owner launches and owns the shared Chrome process. Preferred shape: a **gateway-level coordinator** (not "first-manager-wins") that the first browser-using manager asks to "get-or-launch" the Chrome; it returns a connection target (the `ws://127.0.0.1:9223` URL) and refcounts live agents. Subsequent managers get the existing connection without relaunching. The coordinator owns shutdown: the Chrome is killed when the gateway stops (reuse the existing `Shutdown`→`allocCancel` discipline, ADR-038 D4 post-review amendment) or when the last agent detaches (configurable). The bootstrap race (two managers both see no Chrome and both try to launch) is serialized by the coordinator's own mutex **plus** the existing `checkDebugPortAvailable` preflight as a backstop diagnostic — the loser connects instead of erroring. **Confidence: Medium** (election race is the fiddliest part; a gateway-owned coordinator is cleaner than peer election but is new surface in `loop.go`/`gateway.go`).

### D5 — Sandbox unchanged (no range expansion)
One Chrome on 9223 keeps the existing fixed-port allow-list (`sandbox_apply.go:388,419`) byte-for-byte. No `9223–9230` range, no widened attack surface. This is a primary advantage of the hybrid over Option B. **Confidence: High.**

### D6 — RAM framing (why not Option B)
One managed Chrome already exceeds the *spirit* of Hard Constraint #3 (<10 MB security-feature RAM overhead) — accepted when ADR-038 shipped (Chrome's RSS is runtime working set, not "security-feature overhead"). The hybrid does **not** make this worse: it stays at one Chrome. Option B (per-agent ports + profiles) would multiply resident Chrome by the number of browser-using agents (2 default, +1 per custom agent), which is indefensible for a self-hosted single-binary product. **Confidence: High.**

## Consequences
**Positive:** all browser-using agents browse concurrently; one Chrome (RAM stays flat vs today, beats B's N×); sandbox allow-list unchanged; the cross-agent cookie/login sharing the operator feared is *mitigated* by browser contexts (D2) — strictly better than a shared-everything Chrome; ADR-040/041 models reused, not replaced; ADR-042 provisioning untouched.

**Risks / must-handle:**
- **Coordinator correctness (D4):** a botched get-or-launch election can double-launch (two Chromes fight for 9223) or orphan the process on shutdown. Must be deadlock-safe per ADR-038 (no manager mutex across the launch/connect CDP call) and leak-free on hot-reload (the D4 amendment's `Shutdown()` discipline extends to the shared Chrome's refcount). Single biggest implementation risk.
- **Browser-context adoption (D2):** mapping ADR-041's per-session tab-set onto per-agent browser contexts, and re-binding the screencast to a context's active tab, is the bulk of the work. Verify contexts don't fight the shared `SingletonLock`/profile (contexts are in-memory partitions, so they should not — but confirm).
- **Shared-process blast radius:** one Chrome crash kills every agent's browsing simultaneously (today only one agent is affected because only one runs). Acceptable for v1; the coordinator must relaunch cleanly.
- **Take-the-wheel multi-agent UX (ADR-040):** when one Chrome serves several agents, the live panel must make unambiguous *which agent's* context the human is driving (the active-tab + agent identity in the frame). The `browser_tabs`/`browser_status` frames already carry `session_id`; D3's per-agent binding is what disambiguates.
- **Contract semantics (D3):** if the wire schema does need a delta, Constraint #8's 5-step process applies (spec first, regen, commit artifacts).

**Accepted limitations:** process-level state (GPU cache, crash dumps) is shared across agents — cookies/localStorage/login are NOT (D2 isolates them). If an operator specifically wants agents to *share* a login (e.g., Jim logs in, Ray reuses it), that becomes a deliberate per-context opt-in (future), not the default.

## Alternatives considered (one line each)
- **A — one shared `BrowserManager`, no per-agent contexts:** loses the per-agent accessor model (ADR-038 D4) AND forces shared-everything cookies — strictly worse than D2's per-agent contexts. Rejected.
- **B — per-agent distinct ports + per-agent `ProfileDir`:** fixes concurrency with isolation preserved, but costs N Chrome processes (RAM, indefensible for self-hosted) and requires widening the sandbox allow-list to a port range (attack surface). Rejected on D6 grounds.
- **D — serialize; others get a clear "set `cdp_url`" error:** the honest stopgap the architect recommended; rejected by the operator in favor of actually fixing concurrency now. (The clear-error string from ADR-042's preflight is retained regardless — it remains the diagnostic if the coordinator itself is unavailable.)

## Gaps & ambiguities (to resolve in `/plan-spec` / a spike)
- **[UNKNOWN]** Whether adopting browser contexts forces any change to the ADR-041 tab-adoption/screencast-re-bind paths (contexts are transparent to `Target` events in CDP, but Omnipus's `sessions` map + `activeIdx` bookkeeping is keyed by sessionID today). A spike before plan-spec is warranted.
- **[UNKNOWN]** Exact coordinator placement: gateway-owned type in `pkg/gateway` vs a shared singleton in `pkg/tools/browser` constructed by the first manager. Affects testability and the import graph.
- **[INFERENCE]** The `browser_*` wire schema likely needs no field changes (agent identity already present), but the binding semantics change — confirm by auditing every frame consumer (SPA + gateway WS) in plan-spec.
- **[ASSUMPTION]** Browser contexts isolate cookies/localStorage/indexedDB/cache but are NOT separate renderer processes; a renderer compromise is therefore not isolated per agent. Out of scope for this ADR (true of N Chromes only partially, and the threat model is agent-isolation-from-agent, not browser-RCE).

## Confidence (per decision)
- **D1 shared Chrome + connect:** **High.** Basis: remote-allocator path exists in code; auto-detect is net-new but well-scoped. Missing: the precise dial-vs-launch probe.
- **D2 browser contexts for isolation:** **High** (primitive: `WithNewBrowserContext` verified in chromedp 0.15.1 + cdproto), **Medium** (clean adoption into Omnipus's architecture — needs the spike above). Improvement path: spike tab-set-on-context before plan-spec.
- **D3 per-agent session identity:** **Medium.** Basis: agent identity already on the wire. Missing: confirmed no-schema-change audit.
- **D4 coordinator:** **Medium.** Basis: standard leader/refcount pattern. Missing: chosen placement; election edge cases under hot-reload.
- **D5 sandbox unchanged:** **High.** Verified `sandbox_apply.go:388,419`.
- **D6 RAM framing:** **High.** One Chrome vs N; qualitative.

## Next steps
1. **`/grill-spec` this ADR** (`docs/internal/architecture/ADR-043-browser-shared-chrome-per-agent-contexts.md`) — red-team before committing ~2 sprints; the coordinator (D4) and the browser-context adoption spike (D2 gap) are the highest-value targets.
2. Run the **D2 spike** (tab-set-on-browser-context) and the **D3 contract audit**; feed results into plan-spec.
3. **`/plan-spec`** the chosen direction (BDD/TDD, traceability), then `/taskify` → implement in waves → `/grill-code`, on `bugfixes2` per operator decision, then UAT extending the ADR-040/041 matrix with the multi-agent-concurrent case (the headline test: Jim and Ray browsing in different contexts simultaneously, each isolated, live-view correct per agent).
