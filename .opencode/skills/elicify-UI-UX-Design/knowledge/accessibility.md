# Accessibility — WCAG 2.2 thresholds & 2025–2026 landscape

WCAG 2.2 (W3C Recommendation, Oct 2023) is the operative target through 2026.
Numeric thresholds are normative. Accessibility is a **baseline, not a retrofit**
— and now legally enforced (see bottom).

## Contrast (the legal numbers)

- **Text** (1.4.3, AA): normal ≥ **4.5:1**; large ≥ **3:1**. Large = ≥18pt
  (24px) regular or ≥14pt (18.66px) bold. AAA (1.4.6): 7:1 / 4.5:1.
- **Non-text** (1.4.11, AA): UI component boundaries/states and meaningful icons/
  graphics ≥ **3:1**. A text-only button can pass if its *label* meets 1.4.3.
- Thresholds aren't rounded — 4.499:1 fails. Use markup colors, not screen
  pixels.
- ⚠ **APCA is NOT a confirmed WCAG 3 standard.** WCAG 3.0 is an early draft with
  no contrast algorithm decided (as of 2025–2026). WCAG 2.x ratios remain the
  legally enforceable requirement. APCA's Lc scale is useful *supplementary*
  guidance (better for dark mode), but don't replace 2.x with it.

## Use of color (1.4.1, A)

Never convey info by color alone. Links distinguished by color need a non-color
cue (underline) **+** ≥ 3:1 contrast vs surrounding text, on default and focus.

## Keyboard (2.1.1/2.1.2/2.1.4, A)

- All functionality keyboard-operable; **no focus trap** (Esc/Tab escapes,
  except intentional modal scoping).
- Enter activates links; Enter/Space activate buttons; Space toggles checkboxes;
  arrows move within radio groups/tabs/menus; **Esc closes** overlays.
- ARIA-role controls (`role="button"`) must re-implement native key handling
  manually.
- Verify: unplug the mouse, Tab/Shift+Tab the whole page.

## Focus order & management (2.4.3, A; 3.2.1, A)

- Logical order matching reading/visual order; only interactive elements in the
  tab order.
- **Never positive `tabindex`** (`tabindex="3"`). Use `0` to add, `-1` for
  programmatic-only.
- Composite widgets (tabs/menus/grids): **roving tabindex** or
  `aria-activedescendant`.
- Modals: trap focus while open, move focus in on open, **restore to trigger on
  close**. SPA route changes: move focus to the new heading (`tabindex="-1"` +
  `.focus()`).

## Focus visibility (2.4.7 AA; 2.4.11 AA new; 2.4.13 AAA new)

- Visible indicator must exist — **never `outline:none` without a replacement**.
- **2.4.11 Focus Not Obscured (new in 2.2)**: focused element not *entirely*
  hidden by sticky headers/footers/cookie banners/chat widgets.
- **2.4.13 Focus Appearance**: indicator ≥ 2px-thick perimeter area, ≥ 3:1
  state contrast.
- Pattern: `outline: 3px solid #005fcc; outline-offset: 2px;`

## Skip links (2.4.1, A)

"Skip to main content" as the **first focusable element**, visually hidden until
focused, targeting `<main id="main" tabindex="-1">`. Complement with landmarks +
heading structure.

## Target size (2.5.8 AA new; 2.5.5 AAA)

- **AA**: targets ≥ **24×24 CSS px**, or smaller with enough spacing that a 24px
  circle on each doesn't overlap neighbors.
- **AAA / recommended baseline**: ≥ **44×44 px** (iOS HIG 44pt, Material 48dp).
- Exceptions: inline text links, UA-controlled, essential presentation.

## Dragging & gestures (2.5.7 AA new; 2.5.1 A; 2.5.4 A)

- Every drag (sliders, sortable lists, map pan, drag-drop) needs a **single-
  pointer (tap/click) alternative** unless essential.
- Path/multipoint gestures (pinch, swipe paths) need a single-pointer
  alternative; motion-actuated functions (shake/tilt) need a UI alternative.

## Screen readers & ARIA (4.1.2, A; 1.3.1, A)

- **First Rule of ARIA: native HTML first.** `<button>` beats `<div
  role="button">` — natives bring keyboard + role + state free.
- ⚠ WebAIM Million 2026: pages *using* ARIA averaged **59.1 errors vs 42
  without** — misused ARIA is worse than none.
- Five Rules: prefer native; don't override native semantics; ARIA widgets must
  be keyboard-operable; don't put `aria-hidden`/`role="presentation"` on
  focusable elements; every interactive element needs an accessible name.
- Accessible name priority: `aria-labelledby` > `aria-label` > native label/
  content > `title`. Use `aria-describedby` for supplementary help.
- Landmarks: prefer semantic `<header>`/`<nav>`/`<main>`/`<aside>`/`<footer>`/
  `<form>`. `role="search"` is the one with no native element.
- Live regions: `aria-live="polite"` (non-urgent) / `"assertive"` (urgent);
  `role="status"`/`role="alert"`. Container must exist before content changes.
- 4.1.1 Parsing is **removed** in WCAG 2.2.
- **Test with a real screen reader** (NVDA+Firefox, JAWS+Chrome, VoiceOver+
  Safari) — automated tools catch <40%.

## Cognitive (new 2.2 criteria)

- **3.2.6 Consistent Help (A)**: help mechanisms in the same relative location
  across pages.
- **3.3.7 Redundant Entry (A)**: don't make users re-enter the same info within a
  process (browser autofill doesn't satisfy this — the *site* must).
- **3.3.8 Accessible Authentication (AA)**: no cognitive memory/transcription/
  puzzle test as the *only* login path — support password managers, paste,
  passkeys, email-link.
- Plus: plain language, consistent navigation (3.2.3), multiple ways to find
  content (2.4.5), save/resume, clear errors (3.3.1/3.3.3).

## Motion & seizures (2.3.1 A; 2.2.2 A; 2.3.3 AAA)

- **≤ 3 flashes per second** (test with PEAT).
- Auto-motion/scroll/auto-update > 5 s must be **pausable/stoppable/hideable**.
- Honor `prefers-reduced-motion: reduce` (CSS + JS `matchMedia`); replace motion
  with fade/dissolve, not removal; drop parallax/z-axis. Offer an in-page toggle
  too (many don't know the OS setting exists).

## Text & reflow

- 200% text zoom without loss (1.4.4); usable at 320px width / 400% zoom, no
  2-D scroll (1.4.10); tolerate custom text spacing — line-height 1.5×, para 2×,
  letter 0.12×, word 0.16× (1.4.12).

## Timing (2.2.1, A)

Time limits / session timeouts must be adjustable, extendable, or disable-able
(except real-time/essential).

## 2025–2026 legal landscape

- **EU — European Accessibility Act** (Directive 2019/882): enforcement began
  **28 June 2025**. Private-sector products/services (e-commerce, banking,
  e-books, transport, telecom). Standard EN 301 549 → **WCAG 2.1 AA**.
  Microenterprises exempt; terminals in service until 2030. Penalties vary
  (~€5k–20k/violation), can include market removal.
- **US — ADA Title II** (DOJ rule, Apr 2024): WCAG 2.1 AA for state/local govt.
  Deadlines **24 Apr 2026** (pop ≥ 50k) / 2027 (smaller).
- **US — ADA Title III** (private): no codified standard, but courts treat WCAG
  2.1 AA as the de-facto benchmark; lawsuit volume high.
- **UK**: Equality Act 2010.
