# ADR-038 — Live interactive browser panel (CDP screencast + input injection)

- **Status:** Accepted (build authorized 2026-07-11)
- **Deciders:** Daniel Piatkowski (operator), lead engineering
- **Supersedes / relates to:** builds on the existing `pkg/tools/browser` headless tooling; ADR-037 (delegation) is unrelated (number was the next free one after 037).
- **Target:** `feat/browser` branch; phase deferred ("explore first, decide later") — implemented as a self-contained, config-gated feature that can ship in either the hotfix line or v0.3.

## Context

Omnipus already drives a headless Chromium via `pkg/tools/browser` — 7 agent tools (`browser_navigate/click/type/screenshot/get_text/wait/evaluate`) over one shared persistent Chromium per agent (`BrowserManager`, `manager.go`). The **only** visual feedback today is discrete JPEG screenshots dropped inline into chat (`browser_screenshot` → `media://` → `MediaFrame`). There is no live view and no way for a human to intervene in the agent's browser.

The operator wants a **Codex-style live, interactive browser**: watch the agent drive a page in real time, and *take control* (click/scroll/type) when a human step is needed (login, CAPTCHA, purchase approval), then hand back — all in one shared browser session. Placement decision (operator): an **on-demand overlay panel** over the chat (not a docked split-pane — the SPA has no split-pane primitive and the UI is "chat-first, no separate canvas"), with **pop-out to its own window**, and **fully interactive** (take control), not read-only.

A de-risk spike (2026-07-11, chromedp v0.15.1) **proved both mechanics on `chrome-headless-shell`** (production's default binary): `Page.startScreencast` streams JPEG frames out, and `Input.DispatchMouseEvent`/`InsertText` inject control in. See [[project_browser_live_panel_spike]].

## Decision drivers

- **Hard constraints:** single Go binary, pure Go / no CGo, SPA embedded (Constraints 1–2). Contract-first wire formats (Constraint 8).
- **Chat-first UI, no separate canvas** — overlay, not a persistent docked pane.
- **Security:** "take control" is a real remote-control surface; but the live view renders *pixels* (JPEG frames), never an embedded copy of the target site's live DOM/JS — so there is **no arbitrary-site code to isolate**, which removes the need for the preview second-origin here.
- **Reuse:** the shared `BrowserManager` Chromium, the gorilla WS + first-message-auth pattern, the Radix `Sheet` overlay, the `ws.ts` client class.

## Decisions

### D1 — Transport: a dedicated WebSocket `/api/v1/browser/ws`
A second gorilla WebSocket, **separate** from the chat WS, authenticated by the same first-message `{"type":"auth","token":"…"}` handshake (browsers can't set WS headers; the existing chat WS solves this with an app-level auth frame — we mirror it exactly). Rationale for a separate socket, not multiplexing on chat: screencast is a high-volume, independently-lifecycled stream; keeping it off the chat socket avoids interfering with chat backpressure/replay logic (`websocket.go`'s `sendRawFrameBytes`/replay divert). The `ws.ts` `WsConnection` class is self-contained and supports a second instance cleanly.

**Alternative rejected:** multiplex on chat WS — couples frame volume to chat delivery, complicates replay.

### D2 — Wire frames (contract-first, `contracts/asyncapi.yaml` + `components/schemas/`)
New frames, generated into the shared `WsFrame` union + Zod (never hand-written; Constraint 8). Client→server: `browser_attach` `{session_id, agent_id}`, `browser_input` `{kind, x?, y?, button?, delta_x?, delta_y?, key?, code?, text?, modifiers?}`, `browser_control` `{action: take|release}`, `browser_detach`. Server→client: `browser_frame` `{session_id, seq, data(base64 jpeg), width, height, page_scale, offset_top, scroll_offset_x, scroll_offset_y}`, `browser_status` `{state: attached|idle|controlling|released|detached|error, message?, controller?}`. Reuse existing `auth`/`error` frames.

### D3 — Backend live-view engine
A `browserlive` engine attaches to the session's chromedp context (`mgr.Session(sessionID)`), runs `page.StartScreencast(JPEG, quality≈60, maxW/H=1280/720, everyNthFrame)`, subscribes via `chromedp.ListenTarget` to `page.EventScreencastFrame`, forwards each frame to connected viewers and **acks it** (`page.ScreencastFrameAck`, off the event goroutine — inline ack deadlocks), and dispatches input via `input.DispatchMouseEvent`/`DispatchKeyEvent`/`InsertText`. **Screencast is repaint-driven, not fixed-FPS** — idle pages emit ~1 frame; the client renders a **synthetic cursor** and a periodic status heartbeat covers liveness. One engine per (agent, session), reference-counted by connected viewers; `StopScreencast` when the last viewer detaches.

### D4 — Make `BrowserManager` reachable from the gateway
Today `BrowserManager` is a per-agent private field on `AgentLoop` (`al.browserMgr`), overwritten (and the prior one `Shutdown()`) on each agent's tool registration — there is **no** exported accessor and `al.browserMgr` is not reliably "the" manager. Fix: store managers **per agent** (`map[agentID]*BrowserManager`, populated in `registerSharedTools`, no shutdown-on-overwrite) and add an exported `(*AgentLoop) BrowserManagerForAgent(agentID) (*browser.BrowserManager, bool)`. The gateway WS handler resolves the manager for the attached agent and calls `.Session(sessionID)`. Also fold `--disable-dev-shm-usage` into the ExecAllocator flags (container stability); add `--remote-allow-origins=*` only if screencast requires it on the pinned loopback debug port (verify empirically — the port stays loopback-only + Landlock-gated, never network-exposed).

### D5 — Frontend: `BrowserLivePanel` overlay + second WS client
A right-side `Sheet` overlay (`overlay={false}`, chat stays visible), opened from `ui` store `browserPanel` state, mounted app-root in `AppShell.tsx`. It opens a second `WsConnection` to `/api/v1/browser/ws`, auth-handshakes, sends `browser_attach(activeSessionId, activeAgentId)` (from `useSessionStore`), renders `browser_frame` payloads to an `<img>`/canvas with a synthetic cursor, and captures pointer/keyboard → coord-mapped `browser_input` (map rendered element coords → device coords via frame `page_scale`/dimensions). A **Take control** toggle sends `browser_control`. Launcher: a "Watch live" affordance on *running* browser tool-calls (`BrowserTool.tsx` header) plus optional auto-open-on-first-browse (setting). **Pop-out** = a main-origin SPA route `/browser-live` rendering the panel fullscreen, opened via `window.open` — no isolated origin needed (frames are images, not an embedded site).

### D6 — Take-control gating, turn coordination, security
Input injection is refused unless a prior `browser_control:take` succeeded; the backend authorizes it for the authenticated session owner and records a control-lock on the session. Config: `browser.live_view_enabled` (default true), `browser.take_control_enabled` (default true) — operator can disable either. **Turn coordination (v1, cooperative):** while a human holds control, the agent's browser tools observe the control-lock and return a soft "user is controlling the browser" result rather than fighting for the cursor; the panel shows **"Agent driving" vs "You're driving."** No mid-tool preemption in v1 (documented limitation). Security: frames are pixels (safe to render, sanitized as `data:image/jpeg`); the raw CDP `9223` port is never exposed to the browser (all frames proxied through the gateway); `browser_input` is rate-limited; take/release control is audit-logged.

## Consequences

**Positive:** Codex-grade UX; reuses the existing browser, WS, and overlay infrastructure; pure Go, single binary; the security surface is bounded (image-out, gated input-in). **Negative / risks:** a new authenticated WS + a remote-control surface (needs the 7-reviewer + security pass); repaint-driven frames require a synthetic cursor and heartbeat; the per-agent `BrowserManager` refactor touches shared `loop.go`; turn-coordination is cooperative, not preemptive, in v1.

**Confidence:** *High* on D1–D3 (spike-proven transport + screencast + input). *Medium* on D4 (per-agent manager refactor in shared `loop.go`) and D6 (cooperative turn-coordination UX — the handoff choreography is the main thing to get right in UAT).

## Implementation plan (waves)
1. **Contracts** (this ADR → `asyncapi.yaml` + schemas → regenerate → `make verify-contracts`).
2. **Backend** (manager per-agent accessor + `browserlive` engine + WS handler + config) ∥ **Frontend** (panel + second WS client + store + pop-out route + launcher).
3. **Tests** (Go unit: relay/ack/input/gating; vitest: panel/ws/coord-mapping/control).
4. **7-reviewer gate** → fix wave.
5. **UAT:** ≥2 agents impersonating human testers via Playwright MCP through the Amazon-style shop scenario (watch live → take control → pop-out) → fix wave.
