/**
 * contrast.ts — WCAG 2.2 relative-luminance / contrast-ratio helpers for the
 * non-axe accessibility rows (ADR-068 FR-041, spec §Accessibility, MAJ-012).
 *
 * axe-core has NO rule for focus-ring contrast (2.4.11) — it is deliberately
 * out of its automatable set — so the spec requires a named Playwright
 * assertion instead. These helpers are that assertion's arithmetic, kept in one
 * place so the focus-ring row and any later colour row cannot drift apart.
 *
 * Formulae are WCAG 2.x verbatim:
 *   L  = 0.2126 R + 0.7152 G + 0.0722 B, each channel linearised as
 *        c/12.92 for c <= 0.03928, else ((c + 0.055)/1.055) ^ 2.4
 *   CR = (Lmax + 0.05) / (Lmin + 0.05)
 */

/** An sRGB colour with straight (non-premultiplied) alpha in 0..1. */
export interface Rgba {
  r: number
  g: number
  b: number
  a: number
}

/**
 * Parse the `rgb()` / `rgba()` form `getComputedStyle` always returns in
 * Chromium. Returns null for keywords a computed style can still produce
 * (`transparent` resolves to rgba(0,0,0,0), which parses fine; `currentcolor`
 * and `none` do not and are the caller's signal that no colour was painted).
 */
export function parseRgb(input: string): Rgba | null {
  const m = /^rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:[,/\s]+([\d.]+))?\s*\)$/i.exec(
    input.trim(),
  )
  if (!m) return null
  return {
    r: Number(m[1]),
    g: Number(m[2]),
    b: Number(m[3]),
    a: m[4] === undefined ? 1 : Number(m[4]),
  }
}

/** Composite `fg` over the opaque `bg`, producing an opaque colour. */
export function flatten(fg: Rgba, bg: Rgba): Rgba {
  const a = fg.a
  return {
    r: fg.r * a + bg.r * (1 - a),
    g: fg.g * a + bg.g * (1 - a),
    b: fg.b * a + bg.b * (1 - a),
    a: 1,
  }
}

/** WCAG relative luminance of an opaque sRGB colour. */
export function relativeLuminance({ r, g, b }: Rgba): number {
  const lin = (channel: number) => {
    const c = channel / 255
    return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
  }
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b)
}

/** WCAG contrast ratio between two opaque colours; 1.0 … 21.0. */
export function contrastRatio(a: Rgba, b: Rgba): number {
  const la = relativeLuminance(a)
  const lb = relativeLuminance(b)
  const [hi, lo] = la >= lb ? [la, lb] : [lb, la]
  return (hi + 0.05) / (lo + 0.05)
}

/**
 * What the browser reported for one focused element. Collected inside
 * `page.evaluate` (the DOM side cannot import this module), asserted outside.
 */
export interface FocusRingSample {
  selector: string
  /** `getComputedStyle(el).outlineColor` while focused. */
  outlineColor: string
  outlineStyle: string
  outlineWidth: string
  /** The first opaque background-color walking el → ancestors → <body>. */
  backgroundColor: string
}

/**
 * The contrast of a focus ring against the surface it is drawn on, or null when
 * the sample carries no parseable colour (which the caller must fail on — a
 * ring nobody can measure is a ring nobody can see).
 */
export function focusRingContrast(sample: FocusRingSample): number | null {
  const ring = parseRgb(sample.outlineColor)
  const surface = parseRgb(sample.backgroundColor)
  if (!ring || !surface) return null
  const opaqueSurface = { ...surface, a: 1 }
  return contrastRatio(flatten(ring, opaqueSurface), opaqueSurface)
}
