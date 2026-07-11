# ADR-039 — User-initiated browsing + annotate-a-region-and-discuss

- **Status:** Accepted (build authorized 2026-07-11)
- **Deciders:** Daniel Piatkowski (operator), lead engineering
- **Extends:** [[ADR-038]] (live interactive browser panel). Same `feat/browser` branch, same `/api/v1/browser/ws` + `LiveView` engine + `BrowserLiveView` panel.
- **Motivation:** two natural extensions of the live browser, both requested after ADR-038 shipped: (A) let the *user* start browsing and then hand the session to the agent; (B) Codex-style "mark a region / click an element in the browser and talk about *that* with the agent."

## Context

ADR-038 delivered: the agent drives a shared Chromium tab (`browser.DefaultSessionID`), the user opens a **Live Browser** panel (launched from a "Watch live" button on an agent browser tool-call), watches the CDP screencast, and can **take control** (inject mouse/keyboard) or **pop out** to a new tab. The tab is **one shared, persistent session** — the agent's tools and the user's control drive the same tab, so state handover is symmetric (proven in UAT: user navigates → agent's `browser_get_text` reads the new page).

Two gaps for the requested flows:
- **User-initiated:** the *only* way to open the panel is the "Watch live" button on an *agent* browser tool-call — so the user can't start a browser session themselves. And take-control only lets you click/type on the already-loaded page — there is no way to type a destination URL.
- **Annotate-and-discuss:** research confirms Codex's in-app browser has an **Annotation mode** ("click an element, or drag to select an area, write and save your comment," then ask the agent to address it; plus an "Adjust" style panel). We have no equivalent — but the live view is already a pixel stream, and the user-image → `media://` → provider vision pipeline already works, so this is largely a reuse.

## Decisions

### D-A1 — Persistent "Open browser" launcher
Add an "Open browser" control to the chat top-bar cluster (`ChatControls.tsx`, alongside New Chat / Agent / Model / Sessions) that calls `openBrowserPanel(activeSessionId, activeAgentId)`. The backend `BrowserManager.Session()` lazily creates a blank tab on the WS attach, so opening before the agent has browsed yields a ready blank browser. The launcher is shown only for agents that have browser tools (Ray/Jim by seed; Mia/Ava don't) — the panel already surfaces a `browser_status(error)` for a missing manager as a fallback.

### D-A2 — URL bar via a new `navigate` input kind (with an SSRF gate)
Add `navigate` to `BrowserInputFrame.kind` plus a `url` field (contract-first; **both** `contracts/components/schemas/BrowserInputFrame.yaml` and the hand-duplicated inline copy in `contracts/asyncapi.yaml`). The `LiveView` dispatches it as `page.Navigate(url)` on the tab context. **Security (blocking):** unlike the agent's `browser_navigate` tool (which calls `BrowserManager.ValidateURL` for SSRF/scheme checks), the live-WS input path has no URL gate — so a user-driven `navigate` **MUST** run through the same `ValidateURL` before dispatch, and is honoured only while the viewer holds control (same gate as every other input kind). The panel gets an address bar (`BrowserLiveView` header) enabled only while controlling.

### D-A3 — Handover ("hand to agent")
No engine change — the shared tab already makes handover work (release control → the agent's next tool call drives the same tab). Add a convenience **"Hand to agent"** button that releases control and inserts a short hint into the chat composer ("Continue from the current page: …") so it's one click instead of release-then-type.

### D-B1/B2 — Annotate a region → crop image + comment → agent vision (Codex parity, image path)
A **comment/annotate mode** on the live view (a third interaction state distinct from watching and driving): the user drags a rectangle (or clicks, which selects the element's bounding box via D-B3) over the frame, types a comment, and hits send. The frontend crops that region from the current screencast frame onto a canvas (`ctx.drawImage(sx,sy,sw,sh…)` — same pattern as `media-actions.ts`), builds a `File`, and calls `composerRuntime.addAttachment(file)` — flowing through the **existing** upload → `media://` → `resolveMediaRefs` → provider vision pipeline (zero new agent/vision code). The comment prefills the composer text. Net-new is only the selection UX. This is the true Codex parity ("attach visual comments").

### D-B3 — Best-effort DOM inspect (element text/HTML) to enrich the discussion
Add an authenticated `POST /api/v1/browser/inspect` (OpenAPI contract: `BrowserInspectRequest{session_id, agent_id, x, y, width?, height?}` → `BrowserInspectResponse{text?, html?, tag?, ok}`). The gateway resolves `BrowserManagerForAgent` and runs CDP `DOM.getNodeForLocation` + `DOM.getOuterHTML`/`DOM.getBoxModel` (cdproto/dom is vendored) on the shared tab to extract the clicked/selected element's text + trimmed outer-HTML. The frontend calls it when the user marks a spot and appends the element text to the message as extra context. **Best-effort:** if it fails (cross-origin frame, detached node, timeout) the **image path (D-B2) still delivers the feature** — the inspect result is purely additive. Runs through the deadlock-hardened `LiveView.runCDP`, never a bare `chromedp.Run`, and never holds a mutex across the call.

## Consequences

**Positive:** the browser becomes truly collaborative (either party can start, both share one tab); annotate-and-discuss reaches Codex parity and, by combining the cropped image *and* the element text, can exceed image-only feedback. Reuses ADR-038's engine, the upload/vision pipeline, and the shared-tab handover. **Risks / must-handle:** the user-`navigate` SSRF gate (D-A2) is the one real security surface — non-negotiable. The annotate selection is a new interaction mode that must not collide with the take-control input path. The inspect endpoint is a new authenticated surface (same auth model as the browser WS; best-effort so failures degrade gracefully).

**Confidence:** *High* on D-A1, D-A3, D-B1/B2 (pure reuse of shipped machinery). *Medium* on D-A2 (SSRF gate correctness) and D-B3 (CDP DOM resolution across frame/cross-origin edge cases) — both to be hammered in the UAT edge-case matrix.

## Implementation plan (waves)
1. **Contracts:** `navigate` kind + `url` (asyncapi + components); inspect request/response (openapi). Regenerate → `make verify-contracts`.
2. **Parallel dev:** BE-1 (navigate + SSRF), BE-2 (inspect endpoint, disjoint new files), FE (launcher + URL bar + hand-to-agent + annotate/crop-attach + inspect-call — one owner of `BrowserLiveView`).
3. **Tests** → **7-reviewer gate** → fix.
4. **UAT matrix** (2 independent human-tester agents, edge cases + usability) → fix. No deferrals.

## Post-review amendments (2026-07-11, after the 7-reviewer gate)

Corrections so this record matches the shipped code, plus the fixes folded back:

- **D-B3 as-built:** the inspect endpoint runs `document.elementFromPoint(x,y)` via `chromedp.Evaluate` on the shared tab (CSS-pixel viewport space; text/HTML truncated in-page before returning) — **not** `DOM.getNodeForLocation`/`getOuterHTML`/`getBoxModel`, and `cdproto/dom` is not used. It runs via `BrowserManager.Session` + a `PageTimeout`-bounded `chromedp.Run` (the same lock-free pattern the read-only browser tools use), **not** `LiveView.runCDP`. The request is `BrowserInspectRequest{session_id, agent_id, x, y}` — there are no `width`/`height` fields.
- **D-B1 click behaviour:** a click (sub-threshold drag) synthesizes a small fixed-size box centred on the point — it does **not** select the clicked element's bounding box (element-bounds selection is a deferred enhancement).
- **D-B2 send path:** because `BrowserLivePanel` mounts outside the assistant-ui runtime, the annotation is sent **directly** (`uploadFiles` → media ref → chat-store `sendMessage`), not via `composerRuntime.addAttachment`. Hand-to-agent uses a `composerPrefill` ui-store bridge consumed by `ChatScreen`.
- **D-A1 launcher:** shown for **all** agents (a client-side browser-capability check isn't cheaply available from the agents list); an agent without browser tools yields a clear `browser_status(error)` in the panel — accepted as the v1 behaviour.
- **Fixes applied after the gate:** SSRF `ValidateURL` is now `PageTimeout`-bounded (no DNS hang on the WS read-loop); the annotate/take-control mutual-exclusion race is fixed (the reactive revert that made "Annotate" no-op on first click while driving is removed; guards remain procedural); a `browser_status(error)` no longer drops the local "controlling" state; a zero-axis drag no longer throws (guarded → toast+reset); annotating while a turn is streaming warns instead of silently losing the message; the composer-prefill no longer clobbers a non-empty draft; annotation targets the panel's pinned (session, agent); "Hand to agent" is hidden in the pop-out (where the chat composer is unreachable); inspect logs real CDP errors before collapsing to `ok:false`.
