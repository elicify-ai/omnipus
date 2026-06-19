# Visual System — concrete tokens & numbers

The DESIGN-mode reference. Start a build from these, don't guess. Every value is
defensible; sources noted where specific (Refactoring UI, Material Design 3,
Bringhurst, WebAIM, NN/g, IBM Carbon).

## Typography

**Sizes** — base body 16px web (14px only for dense UI/mobile labels); never
below 12px. Headings 600–700 weight, body 400; avoid <400 for body.

**Modular scale** — pick a base and a ratio; each step multiplies the previous.

| Ratio | Name | Use |
|---|---|---|
| 1.125 | Major second | Dense/mobile UI (Material 3 default) |
| 1.200 | Minor third | Subtle hierarchy |
| 1.250 | Major third | Balanced — common for web |
| 1.333 | Perfect fourth | Strong / editorial |
| 1.618 | Golden | Dramatic / marketing |

Rule: <1.2 for dense UI, 1.15–1.333 most desktop, ≥1.333 for marketing. Use a
smaller ratio on mobile than desktop.

**Line height** — body 1.5–1.6 (unitless); headings/display 1.1–1.3. Bigger text
+ shorter lines → less leading. Increase leading as line length grows.

**Line length (measure)** — 45–75 characters incl. spaces, **~66 ideal**
(Bringhurst); web up to ~85; WCAG 1.4.8 caps at 80; mobile 30–50; multi-column
40–50. CSS: `max-width: 65ch` is a robust default.

**Alignment** — left-align body (consistent return edge); never justify on web
(rivers of whitespace); never center long paragraphs.

**Material 3 baseline scale** (font/line-height px) — production-grade reference:
Display 57/64 · 45/52 · 36/44 · Headline 32/40 · 28/36 · 24/32 · Title 22/28 ·
16/24 · 14/20 · Body 16/24 · 14/20 · 12/16 · Label 14/20 · 12/16 · 11/16.

## Color

- Define a **fixed ramp** up front: 5–10 shades per color (Refactoring UI: ≥5,
  ideally ~9–10). Number 100 (lightest) → 500 (base) → 900 (darkest). Don't
  generate shades on the fly.
- Build order: pick a base (works as a button bg) → find darkest (text) and
  lightest (tinted bg) → fill the middle by eye.
- **60-30-10**: 60% dominant/neutral, 30% secondary surfaces, 10% accent (CTAs,
  focal points). The accent is where the eye lands — keep CTAs in it.
- **2–3 base hues** max + grey ramp + semantic colors (success/warning/danger/
  info).
- Give greys slight saturation (cool `#64748b`, warm `#78716c`); avoid dead
  `#808080`. Reason in HSL, not hex.
- Contrast fix example: `#999` on white = 2.85:1 (fails); `#595959` = 4.5:1
  (passes AA).

## Spacing

- **8px grid** (or 4px for dense/mobile): base unit, every margin/padding/gap a
  multiple — 8, 16, 24, 32, 40, 48, 64.
- **Non-linear scale** with bigger jumps higher up: 4, 8, 12, 16, 24, 32, 48,
  64, 96, 128 (relative difference matters more at larger sizes).
- Start generous, then tighten. Constrain content width; don't force-fill.
- **Unambiguous spacing**: a label sits visibly closer to its owner (more space
  on one side). Internal padding of a list item must differ from the gap between
  items so groups are visible.
- Pair an 8pt layout grid with a 4pt baseline grid for vertical text rhythm.

## Hierarchy

- One primary action per page/section; a few secondary; the rest tertiary.
- **Three levers together**: size (large/base/small) × weight (bold/medium/
  normal) × color (dark/medium-grey/light-grey). Never size alone.
- **De-emphasize to emphasize**: if the primary doesn't stand out, quiet the
  surroundings rather than enlarging it.
- Buttons: primary = filled/solid accent · secondary = outline/muted · tertiary
  = text/link. Don't emphasize destructive actions unless they're the confirmed
  primary action.
- Separate visual from semantic hierarchy (an `<h1>` need not be the biggest
  thing).

## Contrast (WCAG)

- Normal text AA ≥ 4.5:1, AAA ≥ 7:1.
- Large text (≥24px or 18.66px bold) AA ≥ 3:1, AAA ≥ 4.5:1.
- UI components / icons / focus rings / state ≥ 3:1.
- Thresholds are not rounded — 4.499:1 fails 4.5:1. Use markup colors, not
  screen pixels.

## Motion timings

- **Durations**: UI 100–500 ms; micro-interactions <300 ms. Button press
  100–160 ms · tooltip 125–200 ms · dropdown 150–250 ms · modal/drawer
  200–500 ms. **Exit shorter than enter** (e.g. 300/200 ms). Scale duration to
  travel distance.
- **Easing**: `ease-out` default for UI/entrances; **never `ease-in` for
  entrances** (sluggish); `ease-in` only for elements leaving permanently;
  `ease-in-out` for on-screen point-to-point. Prefer custom `cubic-bezier` over
  weak CSS keywords — e.g. IBM Carbon productive `cubic-bezier(0.2,0,0.38,0.9)`.
- **Material 3 tokens**: Standard 300 ms, Standard-decelerate (enter) 250 ms,
  Standard-accelerate (exit) 200 ms, Emphasized 500 ms.
- **Performance**: animate only `transform` and `opacity` (GPU-composited, skip
  layout/paint). 60 fps = ~16 ms frame budget (8–10 ms on low-end mobile).
  Never animate width/height/top/left/margin/padding/font-size.
- **Stagger** multi-element entrances ~20 ms apart; keep the full sequence within
  ~500 ms.
- **Springs** for drag/gesture/"alive" interactions; use `useSpring` for
  mouse-tracked motion (binding 1:1 feels artificial).

## Loading states (which indicator when)

| Wait | Indicator |
|---|---|
| < 1 s (esp. < 300 ms) | Nothing |
| 1–10 s, single blocking action | Spinner |
| 2–10 s, full-page content | **Skeleton** (feels 30–50% faster; ~2× a spinner) |
| > 10 s | Determinate progress bar + estimate + cancel/background |

Skeletons must reveal real content as it arrives (not a splash). Subtle shimmer
further reduces perceived duration. Abandonment: ~3 s frustration, ~5 s many
leave, ~10 s majority abandon.
