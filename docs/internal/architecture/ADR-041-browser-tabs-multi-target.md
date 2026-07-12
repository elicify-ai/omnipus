# ADR-041 — Browser tabs: multi-target sessions (follow new-tab redirects)

- **Status:** Accepted (build authorized 2026-07-12)
- **Deciders:** Daniel Piatkowski (operator), lead engineering
- **Extends:** [[ADR-038]] (live browser) + [[ADR-039]] (user-initiated browsing) + [[ADR-040]] (take-the-wheel). Same `feat/browser` branch, same `LiveView` engine + `BrowserLiveView`/`BrowserLivePanel`.
- **Motivation:** A real appointment-booking session (Ray on `elicify.ai/contact`) proved a hard gap: the "Pick a 30-min slot →" button is `<a href="https://cal.com/…" target="_blank">`. `browser_click` succeeded but the navigation opened a **new tab** the tools never adopt — `BrowserManager` binds exactly **one** chromedp target per session, so the agent was stranded on the contact page and gave up. Any link/`window.open` that opens a new tab is invisible today. Forcing everything into one tab (neutralising `target=_blank`/`window.open`) was rejected as a hack that breaks legitimate popups (OAuth/payment) and discards the browser model. The correct fix is **real multi-tab support**: adopt new targets, switch between them, and show them in the panel.

## Decisions

### D1 — Tab-set session model (active tab)
The primary browsing context becomes an **ordered set of tabs** (chromedp targets) with an **active tab**, instead of a single hardcoded `"default"` target. Concretely, `BrowserManager` gains a browsing-context abstraction: a `[]*tabEntry` (each wrapping a target `ctx`/`cancel`/`targetID`) + an `activeIdx`, guarded by the existing mutex. `Session(defaultSessionID)` returns the **active tab's** context, so **every existing tool and the LiveView keep working unchanged** — they just follow the active tab. `MaxTabs` (existing, default 5) now caps tabs-in-the-set. (The `sessions` map already tolerates multiple targets; this reframes it as a tab set with an active pointer rather than independent sessions.)

### D2 — Adopt new targets + auto-switch + report
When a target is created for the browsing context (a `target="_blank"` click, `window.open`, or `Ctrl/Cmd+click`), the manager **adopts** it: attach a chromedp context to the new target id, append it to the tab set, and by default **make it the active tab**. Detection: a CDP `Target.targetCreated`/`attachedToTarget` listener on the browser (auto-attach to new page targets), reconciled on demand right after a `browser_click`. `browser_click` returns a result that **reports a new tab** when one opened ("opened + switched to tab N: <url>") so the agent knows a redirect happened and is now on it. Deadlock-safety per ADR-038: never hold the manager mutex across a CDP call; bound every CDP round trip with a timeout; adoption is in-memory bookkeeping + a bounded attach.

### D3 — Tab tools (agent API)
Three new `system.*`/builtin browser tools (registered for every agent that has the browser tools; explicit tool-policy entries seeded per Constraint #6):
- `browser_list_tabs` → `[{index, title, url, active}]`.
- `browser_switch_tab {index}` → make tab `index` active (subsequent tools + the live screencast follow it).
- `browser_close_tab {index}` → close/cancel a tab; if it was active, activate a sensible neighbour; never close the last tab into a void (open a blank one or keep one).
Plus `browser_click` new-tab reporting (D2). This directly fixes the booking: the Cal.com tab opens → the agent is auto-switched (or calls `browser_switch_tab`) → picks the slot.

### D4 — Live-panel tab strip (contract-first)
The panel shows a **tab strip** (open tabs: title/favicon + active highlight + close ✕ + a "＋" new-tab), and the **screencast follows the active tab**; clicking a tab switches it. Two AsyncAPI additions (contract-first, since they cross the panel boundary):
- Server→client `browser_tabs` frame: the current tab list + active index (broadcast on any tab open/close/switch/title-change).
- Client→server a tab-switch action (extend the existing control/input channel or a small `browser_switch_tab` frame): switch the active tab for this session.
On switch, the LiveView **re-binds the screencast to the new active target** (stop screencast on the old target, start on the new) — the trickiest piece; must reuse the ADR-038 deadlock-safe `runCDP` path and the last-frame cache so the viewer sees the new tab promptly. The user driving (take-the-wheel input) and the agent's tools both act on the active tab, consistently with ADR-040's control model.

## Consequences
**Positive:** the agent (and the live user) can follow `target="_blank"` / `window.open` redirects — appointment booking and any multi-tab flow work; the panel becomes a real browser with tabs; no hacky suppression of popups. Reuses the existing `sessions`/`MaxTabs` plumbing and the ADR-038 LiveView engine.
**Risks / must-handle:** target adoption must be deadlock-safe (no mutex across CDP; bounded attach) and leak-free (close/cancel adopted targets on tab-close and on session shutdown); the screencast re-bind on switch must not tear down the browsing context or wedge (reuse `runCDP` + last-frame cache); `MaxTabs` enforcement on adoption (a runaway `window.open` loop must be capped, not unbounded); `Session(defaultSessionID)` semantics change from "the one tab" to "the active tab" — audit every caller (all tools + inspect + LiveView) for that assumption; per Constraint #6 the 3 new builtin tools need seeded explicit tool-policy entries for every agent (boot-validation will abort otherwise).
**Deferred:** cross-origin popup edge cases (OAuth handshakes that post back to the opener) may still need the opener tab — acceptable v1 (the tab is adopted; the flow may need manual help); tab drag-reorder; per-tab isolated sessions.

## Implementation plan (waves)
1. **Contracts-first:** the `browser_tabs` frame + the switch action (AsyncAPI + generated Go/TS). `make verify-contracts`.
2. **Parallel dev:**
   - **BE-core:** `BrowserManager` tab-set model (D1) + target adoption/auto-switch + `MaxTabs` cap (D2), keeping `Session(defaultSessionID)` = active tab so existing tools/LiveView are unaffected; audit all `defaultSessionID` callers.
   - **BE-tools:** `browser_list_tabs`/`browser_switch_tab`/`browser_close_tab` + `browser_click` new-tab reporting + the 3 seeded tool-policy entries (Constraint #6) + tool catalog/docs.
   - **BE-liveview + gateway:** broadcast the `browser_tabs` frame; handle the switch action; **re-bind the screencast to the active tab** on switch (D4 backend half).
   - **FE:** the panel **tab strip** + switch + the ＋/✕ affordances, consuming the `browser_tabs` frame (D4 frontend half).
3. **7-reviewer gate** → fix wave (no deferrals).
4. **Deploy + UAT:** extend the matrix with tab edge cases (open-in-new-tab redirect = the booking flow; switch/close; MaxTabs cap; screencast follows active; close-active-tab; the take-the-wheel control model still correct per ADR-040) AND re-run the ADR-040 checks (E1/A8) → 2+ parallel human-tester agents → fix → iterate until CI **and** UAT are green. The **appointment booking on elicify.ai → cal.com is the headline acceptance test.**
