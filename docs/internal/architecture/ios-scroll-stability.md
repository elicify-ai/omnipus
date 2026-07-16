# iOS/iPad Scroll Stability — the four-part recipe (do not deviate)

**Status:** proven stable, operator-confirmed 2026-07-16.
**Commits:** `117553b8` (body lock), `0ebd1d2b` (always-on vv tracking), `75e300d2` (absolute overlays), plus earlier `overscroll-contain` passes.
**Owner files:** `src/styles/globals.css`, `src/components/layout/AppShell.tsx`, `src/components/layout/Sidebar.tsx`.

This bug family (sidebar/header drifting off-screen on iPad, page gaps after
scrolling, keyboard pushing the header away) was fixed and regressed **many
times**. The final, stable solution has four parts that only work **together**.
Every prior regression came from having some parts but not all, or from
"improving" one part in isolation. Read this before touching any of them.

## The four parts

### 1. `body { position: fixed }` — the real scroll lock

```css
/* globals.css */
body {
  position: fixed;
  inset: 0;
  width: 100%;
  height: 100%;
  overflow: hidden;
  overscroll-behavior: none;
}
```

`overflow: hidden` alone does **not** stop iPad/iOS Safari from **panning the
visual viewport** when a scroll gesture lands on non-scrollable chrome (the
sidebar's whitespace, the top menu bar). That pan is not a document scroll —
no overflow rule can prevent it. `position: fixed` on body is the
battle-tested lock: the page itself physically cannot pan; only inner
`overflow` containers scroll.

### 2. Always-on visualViewport tracking (touch devices only)

`AppShell.tsx` publishes `--app-vh` / `--app-top` from `window.visualViewport`
on every `resize`/`scroll` of the visual viewport, and the shell consumes them:

```tsx
<div data-app-shell className="fixed inset-x-0 ..."
     style={{ top: 'var(--app-top, 0px)', height: 'var(--app-vh, 100dvh)' }}>
```

- **Why always-on:** when the on-screen keyboard opens, iOS pans the visual
  viewport to reveal the focused input. The shell must follow (`offsetTop`)
  or the header slides off-screen.
- **Why it doesn't cause drift anymore:** part 1 (body-fixed) means scroll
  gestures can no longer move the visual viewport — only iOS itself (keyboard)
  moves it, and following that is always correct.
- **⚠️ Do NOT reintroduce a "keyboard-only" gate.** A gate comparing
  `window.innerHeight` to `vv.height` was tried (commit `117553b8`) and never
  fired on iOS — with `interactive-widget=resizes-content` those two values
  track each other. The gate silently disabled tracking and the keyboard bug
  returned (reverted in `0ebd1d2b`).
- **Desktop:** tracking is skipped entirely (`pointer: coarse` check) — the
  CSS fallback `100dvh @ 0px` is always correct there, and desktop
  `visualViewport.height` can disagree with the real viewport (scrollbars,
  devtools), which once caused the pinned-sidebar-cut-off bug.

### 3. Overlays anchored to the SHELL, not the layout viewport

The shell follows the **visual** viewport; `position: fixed` elements anchor
to the **layout** viewport. Those are different coordinate systems the moment
iOS pans. A `fixed` overlay therefore stays behind while the shell + header
move — it *appears* to scroll away (the "sidebar scrolls up but the header is
sticky" bug, fixed in `75e300d2`).

**Rule:** anything that must move with the app chrome uses
`position: absolute` and renders inside `[data-app-shell]` (which, being
`position: fixed` itself, is the containing block). This currently applies to
the overlay sidebar and its click-outside backdrop. Radix portals
(dialogs/dropdowns) are exempt — they are short-lived and re-center on open.

### 4. `overscroll-contain` on every inner scroll container

Each scrollable region (sidebar workspace list, search-modal results, slash
menu) carries `overscroll-contain`, so hitting the end of an inner scroll
never chains the gesture upward. Belt-and-suspenders with part 1.

## Regression log (why each part exists)

| Symptom | Missing part | Fixed by |
|---|---|---|
| Sidebar scroll pulls whole page up, gap at bottom | 1, 4 | body-fixed + overscroll-contain |
| Header slides off-screen when keyboard opens | 2 | always-on vv tracking |
| Header stable but overlay sidebar scrolls away | 3 | absolute-in-shell overlays |
| Pinned sidebar cut off at bottom on desktop | 2's desktop skip | `(pointer: coarse)` gate |
| Search modal taller than visible area on iPad | (adjacent) | `dvh` not `vh` for modal max-height |

## When adding new UI

- New overlay/panel that should move with the chrome → `absolute` inside the
  shell, never `fixed`.
- New scrollable region → add `overscroll-contain`.
- Never modify the AppShell visualViewport hook without re-reading this doc.
