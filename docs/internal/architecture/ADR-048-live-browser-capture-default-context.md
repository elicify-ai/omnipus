# ADR-048: Live-browser capture requires the DEFAULT browser context — amends ADR-047 D2 and ADR-043 D2

- **Status:** **Accepted — 2026-07-18** (operator: Daniel Piatkowski — "the shared
  cookie jar is currently ok"; Option A ratified for v1). Option B (per-agent Chrome
  instances, real isolation) is tracked for later in
  [#509](https://github.com/elicify-ai/omnipus/issues/509).
- **Amends:** ADR-047 D2 (capture context topology) and ADR-043 D2/D4/D7 (per-agent
  isolation becomes conditional on capture mode).
- **Deciders:** Daniel Piatkowski (operator) — pending; architect (recommendation).
- **Evidence level:** 1 — verified against real Chrome 150 (Wave-3 e2e, commit
  `687c7c6e`) + codebase facts.

## Context

ADR-047 D2 captures the agent tab via a gateway-owned `chrome.tabCapture` MV3
extension. ADR-043 D2 places each agent's tabs in its own CDP-created browser context
(`Target.createBrowserContext`) for per-agent cookie/login isolation. Wave-3
integration against real Chrome proved these two accepted decisions are mutually
exclusive as built:

- **`chrome.tabCapture` cannot capture a tab living in a CDP-created browser
  context** — `getMediaStreamId({targetTabId})` fails `"Invalid tab specified."`.
  CDP-created contexts are independent off-the-record profiles outside the
  extension's `include_incognito` reach; `enableInIncognito` grants *visibility*
  (`chrome.tabs.query` sees the tabs) but not *capturability*.
  `[FACT — coordinator.go:249-263; commit 687c7c6e]`
- **`chrome-extension://` pages will not load inside a CDP-created context** —
  navigation fails `net::ERR_BLOCKED_BY_CLIENT` even with `enableInIncognito:true`.
  The encoder page is therefore forced into the DEFAULT context regardless.
  `[FACT — capture_session.go:218-232]`

The interim workaround (`OMNIPUS_BROWSER_CAPTURE_DEFAULT_CONTEXT=1`) hosts the
*agent's* session in the default context too, making its tab capturable — at the
cost of ADR-043 D2 isolation (all agents share the default context's cookie
partition; the D4 context-persistence-across-reload and D7 per-agent tab budget no
longer apply per-agent). This directly contradicts ADR-047 §12's assertion that
"ADR-043 per-agent Chrome contexts — all preserved".

## Decision (v1) — PROPOSED

**Accept default-context capture for v1** (Option A), scoped to the
operator-dominant single-browser-agent deployment, with three hard conditions
(Consequences). Per-agent-process isolation (Option B) is recorded as the escape
hatch if cross-agent cookie isolation becomes a hard requirement.

## Options considered

| Option | Capturable? | Per-agent isolation? | Cost | Fit for v1 |
|---|---|---|---|---|
| **A — default-context capture (flag → config)** | Yes | **No** (shared cookie partition) | Minimal; reverses ADR-043 D2 | **Recommended** — matches single-agent dominance + ship priority |
| **B — per-agent user-data-dir Chrome instances** | Yes | Yes | N-Chrome RAM (~4-5 GB @ 10 agents) + sandbox port widening — both rejected in ADR-043; coordinator redesign | Escape hatch, not v1 |
| C1 — `getDisplayMedia` + auto-select-by-title | Yes | Preserved | Title-matching fragile for a navigating tab; consent surface reintroduced | Rejected |
| C2 — per-context extension page | — | — | **Proven dead** (`ERR_BLOCKED_BY_CLIENT`) | Rejected |
| C3 — CDP screencast frames → WebRTC | Yes | Preserved | Reintroduces superseded ADR-044 A2 topology; **video-only → fails FR-A1 audio** | Rejected |
| D — migrate live-viewed tab into default context on attach | Yes | Partial | Racy cross-context tab migration; high complexity | Deferred (possible v0.3) |

## Recommendation & confidence

**Option A for v1, Option B as the recorded escape hatch. Confidence: Medium-High.**
Grounds: single-agent installs dominate; ADR-043 itself framed cross-agent isolation
as an operator-accepted tradeoff, not an inviolable guarantee; Option B's RAM/sandbox
cost was already rejected once on the same profile. Confidence is not High because
Option A ships a real security-posture reversal that must be explicit (config +
warning + audit), and the multi-agent capture-target gap must be closed or fenced.

## Consequences — three hard conditions on Option A

1. **Promote the env flag to a first-class config knob** (e.g.
   `tools.browser.capture_shared_context`) with an explicit isolation warning in the
   schema doc and Settings UI. An undocumented `os.Getenv` is not an acceptable
   surface for an operator opt-in that voids cookie isolation. `[coordinator.go:266]`
2. **Close or fence the multi-agent capture-target gap:** in the shared default
   context the encoder captures the *globally* active tab (`encoder.js:108-120`),
   not the attached agent's tab. With ≥2 browser-capable agents holding live tabs
   (the default seed gives both Jim and Ray browser tools), a human viewing one
   agent can be shown the other's page. v1 fence: deny/stop capture when more than
   one agent has a live browser session (honest state frame + operator log), OR
   wire per-agent tab targeting into `__omnipusCapture`.
3. **The capability classifier must not advertise `Capable=true` when capture
   cannot succeed** (flag off, extension unseeded, etc.) — degradation must be
   explicit, never silent (ADR-047 D3 NFR-A3).

## Amends

- **ADR-047 D2 / §12:** capture requires the DEFAULT context; the "ADR-043 contexts
  preserved" assertion is withdrawn for any WebRTC-captured agent.
- **ADR-043 D2/D4/D7:** per-agent isolation, context persistence across reload, and
  the per-agent tab budget are suspended for agents whose tabs live in the shared
  capture context.
