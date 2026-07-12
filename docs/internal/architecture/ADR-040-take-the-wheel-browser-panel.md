# ADR-040 — "Take the wheel": live-browser panel control redesign

- **Status:** Accepted (build authorized 2026-07-12)
- **Deciders:** Daniel Piatkowski (operator), lead engineering
- **Extends / supersedes:** [[ADR-038]] (live interactive browser panel) + [[ADR-039]] (user-initiated browsing + annotate). Same `feat/browser` branch, same `/api/v1/browser/ws` + `LiveView` engine + `BrowserLiveView`/`BrowserLivePanel`.
- **Motivation:** the operator found the manual↔agent control UX confusing. It exposed **three verbs for one question** — *Take control*, *Release control*, *Hand to agent* — plus a small corner status pill. Research into how OpenAI's browser agents (Operator, now folded into ChatGPT Agent; and Codex's browser) handle this converged on a simpler model: **the agent drives by default; the human steps in for a moment when needed, then hands back** — control transfer is contextual/conversational, not a symmetric button pair.

## Decisions

### D1 — Minimal header
The panel header reduces to exactly: **✕ Close · 📌 Pin/unpin · ✎ Pen (annotate) · 🔗 always-visible URL/search bar**. Remove the `Take control` / `Release control` toggle and the `Hand to agent` button entirely (and their props `onHandToAgent`). "Pop out" stays as a utility affordance but is de-emphasised (small, right cluster) — it is NOT one of the four primary controls. The status **pill** is replaced by the whole-surface signal in D6.

### D2 — Implicit control model (the core change)
Control is **implicit and contextual**, not a persistent toggle. Three states, derived from two existing signals — the **live-view control lock** (already in `LiveView`/`browser_ws`) and the **chat store's `isStreaming`** for this panel's pinned `(sessionId, agentId)` (agent's turn in flight):

| State | When | Page pointer behaviour | Affordance shown |
|-------|------|------------------------|------------------|
| **You're driving** | user holds the control lock | clicks/scroll/keys drive the page | (implicit) |
| **Agent idle** | agent NOT streaming AND user doesn't hold the lock | **first pointer interaction implicitly takes the wheel** (acquire the control lock, then dispatch the input) | none — "click to drive" |
| **Agent working (watch-only)** | agent IS streaming for this (session,agent) | pointer does NOT drive the page; a contextual **"Take over"** button is shown | **Take over** |

- **"Take over"** does two things: (1) **pauses the agent** by invoking the EXISTING chat stop/cancel action for this session (the same one the chat Stop button uses — reuse, do NOT invent a new backend path), and (2) acquires the control lock so the user is now driving. Never let the user's live input and the agent's tool input reach the tab simultaneously — watch-only while streaming is what prevents the fight.
- **Handing back to the agent = the user just sends a chat message** ("continue", "book this one", etc.). The shared tab means the agent resumes on the current page; ADR-039's `browser_screenshot`-reports-URL fix makes this reliable. There is **no persistent "release"/"hand to agent" button.** (A tiny optional "hand back" convenience MAY remain, but it is not required and must not resurrect the three-verb cluster.)
- **Agent-initiated handoff** (agent proactively pauses on a login/CAPTCHA and prompts the user to take over — the Operator pattern) is a **deferred follow-up** (needs agent-side detection); out of scope for this ADR. The user-initiated "Take over (pauses agent)" above is the shipped behaviour.

### D3 — Annotate stays an explicit Pen mode
The implicit drag-to-mark idea was rejected as error-risky (a stray drag while browsing would mark or grab control). Keep annotate as an **explicit ✎ Pen toggle**: pen on → drag a box / click a point over the frame → comment popover → send the cropped image + comment (+ best-effort element text) to the agent, exactly the ADR-039 D-B1/B2/B3 pipeline. Pen is only offered where a chat delivery target exists (the docked panel, not the pop-out — same `canAnnotate` gating as ADR-039). Entering Pen mode implies not-driving (release the lock if held).

### D4 — Pin / side-by-side
A **📌 Pin** toggle in the header switches the panel between two layouts (state persisted in the `ui` store, e.g. `browserPanelPinned`):
- **Unpinned (default):** the current right-side overlay `Sheet` (non-modal, chat visible behind).
- **Pinned:** the panel **docks as a flex column beside the chat** — the chat area shrinks and both are fully usable at once (a real split view, not an overlay). Rendered from `AppShell` as a sibling of the chat region, not inside the `Sheet`. Toggling pin must preserve the live WS connection where practical (avoid tearing down/reopening the browser session on every pin toggle — key the `BrowserLiveView` mount stably across the layout change).
- Naming: "Pin" = dock the panel. It is unrelated to the ✎ Pen (marking). Different buttons, different jobs.

### D5 — Omnibox URL/search bar (always visible)
The address bar is **always rendered** (not only while driving). Behaviour matches Chrome's omnibox (extend `normalizeNavigateUrl`, or a new `resolveOmniboxInput`):
- input parses as a URL / bare domain (has a dot, no spaces, or an explicit scheme) → **navigate** there (existing `navigate` input kind, SSRF-gated);
- otherwise (spaces, or no dot, e.g. "cheap flights to tokyo") → **Google search**: navigate to `https://www.google.com/search?q=<url-encoded input>`.
- Typing in the bar and submitting while the agent is working should first **Take over** (pause + acquire) so the navigation is the user's, consistent with D2. Keep the server SSRF gate as the sole security authority.

### D6 — Visual "who's driving" indicator (replaces the corner pill)
The mode is a property of the **whole surface**, so it can't be missed (change-blindness fix; recognition-over-recall):
- **Breathing glow border** around the whole frame, colour-coded to who drives: **agent-driving = a distinct "assistant" hue** (cool blue/violet); **you're-driving = brand Forge Gold**; watch-only-idle = neutral. Slow pulse = "live", not distracting.
- **Header chip with a pulsing live dot**: e.g. "● Ray is browsing…" (agent) / "● You're driving" (you) — words + icon back up the colour for accessibility (never colour alone; WCAG).
- **Hover cursor** over the frame while **watch-only** becomes a "👁 watching" (or `not-allowed`) cursor, with the **Take over** button adjacent — so the instant the user moves to interfere they understand it's watch-only and how to cut in.
- **Deferred stretch:** an animated **agent-cursor overlay** (a labelled ghost pointer + click-ripple at the agent's action coordinates) — high delight, but needs the backend to broadcast the agent tools' click coords over the WS, so it is a **follow-up**, not part of this ADR.

## The BrowserLivePanel ↔ BrowserLiveView seam (so parallel work doesn't collide)
Two owners implement in parallel; this is the fixed contract between them:
- **`BrowserLivePanel.tsx` owner** owns: the header **Pin** control, the pinned-vs-overlay layout (with `AppShell` + `ui` store), which optional capability props it passes down, `onClose`/`onPopOut`, and the annotate-capability gate (`canAnnotate`). It does NOT touch the view internals.
- **`BrowserLiveView.tsx` owner** owns: the implicit control-model state machine (D2), the header's **Pen** + **Close/Pop-out** rendering + the always-on **omnibox** (D5), the **Take over** button, the pen/annotate interactions (D3), and the D6 visuals (glow/chip/hover-cursor). It consumes a small, stable prop surface:
  - `sessionId`, `agentId` (unchanged), `onClose?`, `onPopOut?`, `canAnnotate?` (kept), plus `isPinned?: boolean` (display-only, e.g. hide Pop-out when pinned) — the view stays otherwise agnostic to the layout.
  - Remove `onHandToAgent` from the interface.
- "Agent working" is read by the view from the chat store (`useChatStore` `isStreaming` for the pinned session), and "Take over" calls the chat store's existing stop/cancel — both are store reads/actions, no new prop.

## Consequences
**Positive:** ~6 competing controls → **one implicit drive model + a 4-item header**; the mode is always visible on the surface; no accidental agent/user input fights (watch-only while streaming); pin gives a real side-by-side workspace; the omnibox behaves like a browser people already know (mental-model match). Almost entirely **frontend-only** — reuses the existing control-lock WS, chat `isStreaming`, chat stop/cancel, and the ADR-039 annotate pipeline; **no contract or backend changes** for the shipped scope.
**Risks / must-handle:** the implicit "first interaction takes the wheel" must not double-fire or race the lock acquisition; "Take over" must reliably pause the agent (via a **session-scoped** cancel of the panel's pinned session — `cancelStream(sessionId)`, NOT the foreground-session default — since D4's side-by-side makes panel-session ≠ active-chat-session routine); the omnibox search must stay behind the SSRF gate.
**Known v1 limitation (per D4's "where practical"):** toggling 📌 Pin **does** tear down + reconnect the live WS — the Sheet↔docked branch is a root-element-type swap that React always remounts, so `key`-stability can't preserve it. The cost is a brief reconnect flicker + re-acquiring the drive lock, NOT loss of browser/tab state (`browser_detach` drops only the viewer subscription, not the `BrowserManager` session). A stable-mount + portal approach that survives the toggle is a deferred follow-up, not v1 scope.
**Deferred follow-ups (own ADRs/issues):** agent-initiated handoff (pause-and-ask on login/CAPTCHA); animated agent-cursor overlay (needs backend coord broadcast).

## Implementation plan (waves)
1. **Wave 1 — parallel dev:** (A) `BrowserLiveView` owner — control model + header + omnibox + pen + D6 visuals; (B) `BrowserLivePanel`/layout owner — Pin + side-by-side + `ui` store + `AppShell` + route; (C, small) omnibox helper (`browserLiveUrl`) + `ui`-store pin state if not folded into A/B. Disjoint files; code to the seam above.
2. **7-reviewer gate** on the whole diff → fix wave (no deferrals).
3. **UAT:** extend the matrix with the new model's edge cases (implicit take-the-wheel, take-over-pauses-agent, watch-only-blocks-input, pin side-by-side, omnibox URL-vs-search, hand-back-by-message, visual-state correctness) → 2+ parallel human-tester agents → fix → iterate until CI **and** UAT are green.
