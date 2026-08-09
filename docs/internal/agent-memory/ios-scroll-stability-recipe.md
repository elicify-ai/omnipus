---
name: ios-scroll-stability-recipe
description: The proven 4-part iOS/iPad scroll-stability recipe for the Omnipus SPA shell — battle-tested after many regressions; do not deviate
metadata: 
  node_type: memory
  type: project
  originSessionId: 45eb163f-c6f7-4e88-8de3-bcc8667cefc7
---

The Omnipus SPA's iOS/iPad scroll stability was broken and re-fixed many times until 2026-07-16, when the operator confirmed "very stable now". The working recipe has FOUR parts that only work TOGETHER (commits `117553b8`, `0ebd1d2b`, `75e300d2`; full write-up in `docs/internal/architecture/ios-scroll-stability.md`):

1. **`body { position: fixed; inset: 0 }`** (`src/styles/globals.css`) — `overflow: hidden` alone does NOT stop iOS Safari from panning the visual viewport when a scroll gesture lands on non-scrollable chrome. position:fixed is the real lock.
2. **FOCUS-GATED visualViewport tracking on touch devices** (`src/components/layout/AppShell.tsx` publishes `--app-vh`/`--app-top` only while an editable element has focus; focusin/focusout re-evaluate). Two failed alternatives, never resurrect: (a) height-math gate (innerHeight vs vv.height) never fires on iOS → header slides off under keyboard; (b) always-on tracking latches a stale short vv.height after keyboard close → shell permanently shortened (IMG_0616). Desktop skips tracking via `(pointer: coarse)` — CSS fallback `100dvh @ 0`.
3. **Overlays that must move with the shell use `position: absolute` INSIDE the shell, never `position: fixed`** — the shell follows the VISUAL viewport, `fixed` anchors to the LAYOUT viewport; a fixed overlay stays behind during a pan and appears to scroll away while the header stays (the sidebar bug). The shell (`[data-app-shell]`, itself position:fixed) is the containing block.
4. **`overscroll-contain` on every inner scroll container** (sidebar list, search results, slash menu) — stops scroll chaining at the boundary.

**Why:** each part covers a distinct iOS behavior; removing any one reintroduces a specific regression (documented in the doc's regression log).

**How to apply:** when adding any new overlay/panel to the shell, use `absolute` positioning inside `[data-app-shell]`; when adding any scrollable region, add `overscroll-contain`; never touch the AppShell vv hook without re-reading the doc.
