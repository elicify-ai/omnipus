# iOS/iPad Scroll Stability — the four-part recipe (do not deviate)

**Status:** proven stable, operator-confirmed 2026-07-20.
**Commits:** `117553b8` (body lock), `0ebd1d2b` (always-on vv tracking — first attempt, regressed by the stale-height bug below), `dec7713b` (focus-gated vv tracking — **ITSELF A REGRESSION, do not resurrect**, see below), `75e300d2` (absolute overlays), 2026-07-20 fix (always-on tracking restored with a deterministic stale-height guard + trailing re-read — the canonical mechanism for part 2), plus earlier/later `overscroll-contain` passes.
**Owner files:** `src/styles/globals.css`, `src/components/layout/AppShell.tsx`, `src/components/layout/appShellViewport.ts`, `src/components/layout/Sidebar.tsx`.

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

### 2. ALWAYS-ON visualViewport tracking with a deterministic stale-height guard (touch devices only)

`AppShell.tsx` publishes `--app-vh` / `--app-top` from `window.visualViewport`
on every `resize`/`scroll` of the visual viewport, and the shell consumes them:

```tsx
<div data-app-shell className="fixed inset-x-0 ..."
     style={{ top: 'var(--app-top, 0px)', height: 'var(--app-vh, 100dvh)' }}>
```

The math itself is a pure, directly-unit-tested function —
`computeAppMetrics(vv, innerHeight)` in `src/components/layout/appShellViewport.ts`
(see `AppShell.viewport.test.ts`). It has **no dependency on
`document.activeElement`** at all.

- **`--app-top` is ALWAYS set, unconditionally, from `vv.offsetTop`.** There
  is no gate. iOS pans the visual viewport — independent of what has focus —
  whenever the keyboard opens, and (empirically, 2026-07) whenever a tap
  lands on non-scrollable/non-editable chrome too (a message bubble, a tab,
  empty space). The shell must mirror `offsetTop` on every one of those
  events, not just the keyboard-focus ones, or the header drifts out of the
  visible area the instant focus leaves an editable.
- **`--app-vh` uses a DETERMINISTIC height-based guard, not focus:** the
  keyboard is treated as closed when `|vv.height - innerHeight| < 2` (px
  rounding/toolbar-jitter tolerance) — in which case `--app-vh` is REMOVED
  so the CSS fallback (`100dvh`) rules. Otherwise `--app-vh` is set to
  `vv.height`. This is what stops a stale short height from latching after
  the keyboard closes (see the `IMG_0616` regression below) — **without**
  tying it to focus.
- **Trailing re-read on `focusout`:** iOS can occasionally drop the final
  `resize` event right as the keyboard closes on blur. Instead of gating
  tracking off to sidestep that miss, `AppShell.tsx`'s effect schedules a
  ~250ms re-read (`applyMetrics()`) on `focusout` that recomputes the vars
  from whatever `visualViewport` reports once things have settled. This is
  the direct fix for the exact case `dec7713b` was papering over — it
  targets the missed event, not the whole tracking mechanism.
- **Desktop:** tracking is skipped entirely (`pointer: coarse` check) — the
  CSS fallback `100dvh @ 0px` is always correct there, and desktop
  `visualViewport.height` can disagree with the real viewport (scrollbars,
  devtools), which once caused the pinned-sidebar-cut-off bug.

#### ⚠️ Do NOT reintroduce a keyboard-only / focus gate — this is not a suggestion

This has now regressed from **both directions** and both are logged below:

- **Focus-gating removed (`dec7713b`, live 2026-07-16 → 2026-07-20):** the
  vars were published only while an editable had focus. The moment focus
  moved to *anything* non-editable — tapping a message, a tab, empty space —
  the vars were REMOVED and the fixed shell snapped to `top:0` / `100dvh`
  while the visual viewport was still panned. **The header jumped out of the
  viewable area, and tapping any non-editable element reproduced it again.**
  This was reported as a confirmed regression and is the reason this doc's
  part 2 was rewritten.
- **A height-math gate comparing `innerHeight` to `vv.height` (pre-`0ebd1d2b`)**
  never fired on iOS at all — those two values track each other under
  `interactive-widget=resizes-content` — so tracking was silently disabled
  and the header slid off under the keyboard.

Any future "gate publishing on some signal" design is very likely to
reproduce one of these two failures. If tracking needs to change again, keep
`--app-top` unconditional and only ever tighten the **height math** that
drives `--app-vh` (e.g. adjust the `2px` tolerance) — never make either var's
publication depend on `document.activeElement` / focus/blur events. Focus
events are legitimate as a trigger to **re-read** (see the trailing re-read
above), never as a condition on **whether** to publish.

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
| Header slides off-screen when keyboard opens (height-math gate never fired on iOS) | 2 | always-on vv tracking (`0ebd1d2b`) |
| Header stable but overlay sidebar scrolls away | 3 | absolute-in-shell overlays |
| Pinned sidebar cut off at bottom on desktop | 2's desktop skip | `(pointer: coarse)` gate |
| Shell permanently shortened after keyboard close (stale vv.height, `IMG_0616`) | 2's always-on tracking (first attempt, no guard) | focus-gated tracking (`dec7713b`) — **this "fix" was itself a regression, see next row** |
| Header jumps out of the viewable area; tapping any non-editable element reproduces it | `dec7713b`'s focus gate removed `--app-top`/`--app-vh` the instant focus left an editable | always-on tracking **restored**, with a deterministic height-math guard (`|vv.height − innerHeight| < 2px`) for `--app-vh` instead of a focus gate, plus a `focusout` trailing re-read (~250ms) to catch iOS's occasionally-dropped final `resize` — 2026-07-20 fix, current canonical mechanism. Regression test: `AppShell.viewport.test.ts` (locks `--app-top` always-on; fails immediately if a focus gate is reintroduced). |
| Search modal taller than visible area on iPad | (adjacent) | `dvh` not `vh` for modal max-height |

## When adding new UI

- New overlay/panel that should move with the chrome → `absolute` inside the
  shell, never `fixed`.
- New scrollable region → add `overscroll-contain`. FullCalendar's internal
  `.fc-scroller` elements aren't reachable via a React prop, so their
  containment is applied via CSS in `src/styles/fullcalendar-theme.css`
  instead of a duplicate wrapping scroll container.
- **Never modify the AppShell visualViewport hook without re-reading this
  doc — and never gate `--app-top` or `--app-vh` publication on focus/blur.**
  This exact mistake has shipped and regressed twice already (see the
  regression log above). Run `AppShell.viewport.test.ts` after any change to
  `appShellViewport.ts` or the effect in `AppShell.tsx`.
