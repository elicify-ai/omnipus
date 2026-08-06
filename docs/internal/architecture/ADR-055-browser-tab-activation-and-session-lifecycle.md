# ADR-055 — Browser tab activation and browsing-context lifecycle

- **Status:** Accepted
- **Date:** 2026-08-03
- **Supersedes / amends:** none (extends ADR-041 tab sets, ADR-047 WebRTC capture)

## Context

Two independent defects in the browser live panel, both found by analysing an
operator recording (`0803.mov`) and then reproduced live on UAT v36 with
Playwright measurements.

### Defect 1 — tab switch kept streaming the previous tab

Switching tabs updated **only** `sessionEntry.activeIdx` inside
`BrowserManager`. Chrome was never told the active tab had moved.

The WebRTC capture path resolves its capture target in the extension, with
`chrome.tabs.query({active: true, lastFocusedWindow: true})`
(`captureext/embedded/encoder.js` `findActiveTargetTab`). With Chrome's own
notion of "active" unchanged, every recapture re-bound `chrome.tabCapture` to
the tab the user had just switched **away from**.

Measured live (UAT v36, `admin`):

| Surface | Reported |
|---|---|
| Tab strip | `Google` (`aria-pressed=true`) |
| URL bar | `https://en.wikipedia.org/wiki/Octopus` |
| Video stream | Wikipedia pixels (luma 231, unchanged 12s+) |

A three-way desync. The failure was **completely silent**: the WS frame
(`browser_tab_action{switch,index:0}`) was sent and accepted, the video track
stayed `readyState:"live"`, `muted:false`, and there were **zero console
errors**. The only downstream signal was a stalled-RTP watchdog warning in the
gateway log.

The JPEG screencast path never had this bug because it calls
`page.BringToFront()` itself before every `StartScreencast`
(`live.go` `attach` / `rebindScreencastOnce`). The WebRTC path called
`BringToFront` only at capture **start** (`capture_session.go`), never on a
switch. That asymmetry is what hid the defect.

**Why existing tests missed it.** `SwitchTab` was covered
(`TestSwitchTab_ChangesActiveIndex`) and the recapture was covered
(`TestCaptureSession_RecapturePropagatesToRelayAndIngest`) — but nothing
exercised them **together**. The defect lived exactly in the seam between two
well-tested halves, which isolation tests cannot reach by construction.

### Defect 2 — browsing contexts were never reaped

Closing the live panel is a pure UI dismiss: the SPA sends no shutdown frame,
and `browser.CloseSession` had **zero production callers**. A browsing context
— and its resident Chrome — therefore outlived the panel indefinitely.
Reopening the panel days later showed the exact page the user had left.

### Defect 3 — the first-frame wait never ended

The panel showed `Waiting for the first frame…` indefinitely and then fell to
indistinguishable black. Because the connection looked healthy in every
observable way, that state was visually identical to "still loading". The
silence *was* the bug.

## Decision

### D1 — The server activates the tab in Chrome on every switch

`SwitchTab` calls `activateTabInChrome` (CDP `Page.bringToFront`) on the
newly-active tab's context, **before** `notifyTabsChanged`.

Ordering is load-bearing: the tabs-changed callback is what triggers the WebRTC
recapture, so Chrome must already agree about the active tab by the time it
fires. Running activation afterwards would leave the same bug, merely narrowed
to a race window.

Activation is **best-effort and non-fatal**. The switch is already recorded in
`activeIdx`, so every server-side consumer (`Session()`, tool calls, the JPEG
path) still follows the new tab correctly; only the WebRTC capture's own tab
resolution degrades. A failed activation must never turn a successful switch
into a user-visible error.

Runs with **no** `BrowserManager` lock held (ADR-038), like every other CDP
call in that file.

**Rejected alternative — pass the intended tab in the recapture frame.**
Adding `target_url` to `BrowserCaptureControlFrame` so the encoder could prefer
a matching tab was prototyped and reverted. It is defence-in-depth against a
race that D1 closes at its source, and it would have made the encoder's target
resolution depend on two disagreeing authorities instead of one. Revisit only
if a race survives D1 in practice.

### D2 — Idle browsing contexts are reaped, gated on BOTH signals

> **AMENDED 2026-08-05 (operator directive, issue #592).** Reaping is now **per
> TAB**, not per browsing context, and the defaults changed: `idle_ttl` **30m →
> 5m**, gateway sweep **5m → 1m**.
>
> - Each tab is judged on its own last-touched time. A context is torn down only
>   once every tab in it has gone.
> - A context with an attached live-panel viewer is exempt **in full** — the
>   panel's tab strip lists every tab, so all of them count as "open in the UI",
>   and tabs vanishing under a watching user is unacceptable. Viewer *detach*
>   restarts the clock, so the 5 minutes begin when the panel closes.
> - The sweep interval must stay well under the TTL. At 5m/5m a "5 minute"
>   cleanup really means 5-10, because a tab going idle just after a sweep waits
>   out the TTL plus the remainder of the interval.
> - A session that reaches zero tabs carries its own `emptySince` stamp; without
>   it, moving the clock to tabs would leave an empty session with no clock at
>   all and therefore unreapable forever.
>
> The "BOTH signals" framing below is superseded: viewer presence is now a
> whole-context override, and the second signal is per-tab rather than
> per-context.


`ReapIdleSessions` closes a browsing context only when it has had **no attached
live-panel viewer AND no agent tool call** for `tools.browser.idle_ttl`
(default 30m; `<= 0` disables).

Both halves are required, and this is the crux of the design:

- **Viewer-only** would reap an agent legitimately mid-task in an unwatched
  tab — strictly worse than the stale tab this fixes.
- **Tool-call-only** would reap a context a user is actively staring at.

Viewer activity is tracked via `LiveViewRegistry.Attach`/`Detach`; agent
activity via `Session()`. Detach itself counts as activity, so the idle clock
starts when the last viewer leaves — the "user closed the panel" case this
exists for. The gateway sweeps every 5 minutes.

A session with an unstamped `lastActivity` is **adopted** by a sweep (stamped,
judged next time), never treated as infinitely idle — otherwise a context
created microseconds ago by a path that forgot to stamp it would be reaped
immediately.

### D3 — Fresh tabs open a start page, not `about:blank`

`tools.browser.start_page_url` (empty → `about:blank`, preserving the previous
behaviour exactly). A blank void is indistinguishable from a broken panel,
which is precisely the confusion this surface does not need.

### D4 — The first-frame wait has a deadline

After `FIRST_FRAME_TIMEOUT_MS` (15s — generous enough for a cold Chrome launch
plus WebRTC negotiation) the panel surfaces an actionable error. The deadline
is armed only once connected (a slow connect is not a first-frame failure) and
clears if a later frame recovers the stream, so a successful recapture is not
left showing a stale error. A real transport or `browser_status` error still
wins — a specific cause beats the generic message.

## Consequences

- A tab switch now costs one extra CDP round-trip. Bounded by `PageTimeout`,
  off the manager lock, and failure-tolerant.
- Chrome may steal window focus on switch. Already true of the JPEG path via
  its own `BringToFront`; headless is the deployed configuration.
- A long-idle browsing context is closed and reopens cold. This is the intended
  trade: the previous behaviour leaked a Chrome process indefinitely.
- Operators wanting the old never-reap behaviour set `idle_ttl` to a negative
  value.

## Testing

The seam-blindness that let Defect 1 ship is addressed directly:

- `switch_tab_activation_test.go` (6) — activation happens, targets the correct
  tab, precedes tabs-changed, is skipped on lookup failure and dead contexts,
  is non-fatal, and holds no lock.
- `switch_tab_capture_chain_test.go` (5) — the **whole** chain
  `SwitchTab → activate → notifyTabsChanged → onTabsChanged → Recapture`,
  including rapid-switch sequences and hung-activation isolation.
- `idle_reaper_test.go` (12) — both idle signals, viewer-count underflow,
  disable switch, unstamped adoption, start-page fallbacks.
- `BrowserLiveView.firstFrameTimeout.test.tsx` (6) — timeout fires, does not
  fire while connecting or when a frame beats it, recovers on a later frame,
  and both error-precedence cases.

Every behavioural test in these files was **verified to fail without its fix**,
not merely to pass with it.
