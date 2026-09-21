# Card

Card is a public presentation primitive in the Omnipus design system: the bordered panel that groups related content. It consumes shared foundation tokens only.

## Contract

- Variants:
  - `default` — flat panel: `rounded-lg`, border, `--color-surface-1`, no shadow. The app's dominant hand-built panel (measured 2026-09-21: 93 of 103 card surfaces use `rounded-lg`, 98 of 103 have no shadow).
  - `inset` — the same panel on `--color-surface-2`, for a panel nested inside another surface.
  - `floating` — `rounded-xl` with the `--elevation-floating` shadow, for content that floats above a canvas (graph nodes, popover-like cards).
- Sizes: default, dense.
- States: default.
- Sub-parts: `CardHeader`, `CardContent` and `CardFooter` pad with `--space-3`; `CardTitle` uses the body-compact size, semibold; `CardDescription` uses the utility-xs size in the muted colour.
- Public exports: Card, cardVariants, CardHeader, CardFooter, CardTitle, CardDescription, CardContent; type CardProps.
- Theme: dark.

The colocated stories cover every declared variant, size and state, plus dense and narrow-viewport presentations. The manifest maps applicable unit, accessibility, keyboard, pointer, motion, forced-colour, root-size, zoom and reflow checks to executable evidence.
