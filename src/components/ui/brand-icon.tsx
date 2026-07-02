// brand-icon.tsx — Shared <BrandIcon> component for ADR-031 (Track 1 foundation).
//
// Slug convention (logoSlug):
//   Provider slugs:  openai | anthropic | zhipu | kimi | moonshot | qwen | minimax |
//                    deepseek | gemini | groq | mistral | openrouter
//   Channel slugs:   telegram | discord | matrix | whatsapp | googlechat | line |
//                    wechat | tencentqq
//
// The backend catalog agent must use EXACTLY these slug values in the `logoSlug` field.
// They correspond to the vendored SVG filenames with the `p_` / `c_` prefix and `.svg`
// suffix stripped (e.g. `p_openai.svg` → slug "openai").
//
// Both the `p_<slug>` and `c_<slug>` naming conventions are supported; the map is
// keyed by slug only, so the prefix-stripping is done at module init.

import { memo } from 'react'
import DOMPurify from 'dompurify'

// Import all brand-logo SVGs as raw strings via Vite's ?raw query.
// Keys are relative paths like `../../assets/brand-logos/p_openai.svg`.
const rawSvgModules = import.meta.glob<{ default: string }>(
  '../../assets/brand-logos/*.svg',
  { query: '?raw', eager: true }
)

// Build a slug → sanitized-SVG-string map at module init (once, not per render).
// Strip the directory prefix, the `p_` / `c_` channel/provider prefix, and the `.svg`
// suffix to produce the canonical slug.
function buildSanitizedMap(): Map<string, string> {
  const map = new Map<string, string>()
  for (const [path, mod] of Object.entries(rawSvgModules)) {
    // e.g. "../../assets/brand-logos/p_openai.svg"
    const filename = path.split('/').pop() ?? ''
    // Strip p_ / c_ prefix and .svg suffix
    const slug = filename.replace(/^[pc]_/, '').replace(/\.svg$/, '')
    if (!slug) continue
    const raw = typeof mod === 'string' ? mod : (mod as { default: string }).default
    if (!raw) continue
    const sanitized = DOMPurify.sanitize(raw, {
      USE_PROFILES: { svg: true, svgFilters: true },
      // Drop <title>/<desc>: the wrapper <span> already carries the accessible
      // name via aria-label (or aria-hidden when decorative), so an inner SVG
      // <title> is redundant for AT AND leaks the brand name into the DOM as
      // findable text — which double-matches getByText('OpenAI') in consumers
      // that also render the brand name as adjacent text.
      FORBID_TAGS: ['title', 'desc'],
    })
    map.set(slug, sanitized)
  }
  return map
}

const SANITIZED_SVG_MAP: Map<string, string> = buildSanitizedMap()

// ---------------------------------------------------------------------------
// LettermarkChip — fallback when slug has no vendored asset.
// Renders the first 1–2 uppercase letters of the slug in a brand-styled chip.
// ---------------------------------------------------------------------------

interface LettermarkChipProps {
  slug: string
  size: number
  className?: string
}

function LettermarkChip({ slug, size, className }: LettermarkChipProps) {
  const letters = slug.replace(/[^a-zA-Z0-9]/g, '').slice(0, 2).toUpperCase()
  const fontSize = Math.round(size * 0.42)
  return (
    <span
      className={className}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: size,
        height: size,
        minWidth: size,
        borderRadius: Math.round(size * 0.22),
        background: 'var(--color-accent, #D4AF37)',
        color: 'var(--color-primary, #0A0A0B)',
        fontFamily: 'var(--font-outfit, Outfit, sans-serif)',
        fontWeight: 700,
        fontSize,
        lineHeight: 1,
        userSelect: 'none',
        flexShrink: 0,
      }}
      aria-hidden="true"
    >
      {letters || '?'}
    </span>
  )
}

// ---------------------------------------------------------------------------
// BrandIcon — public component
// ---------------------------------------------------------------------------

interface BrandIconProps {
  /** logoSlug — e.g. "openai", "anthropic", "telegram". See slug convention above. */
  slug: string
  /** Icon size in px (width and height). Defaults to 24. */
  size?: number
  /** Additional CSS class applied to the wrapper element. */
  className?: string
  /**
   * Accessible label for the icon (defaults to the slug).
   * Set to undefined or "" to apply aria-hidden (decorative use).
   * Use the `decorative` prop instead if the icon is always decorative.
   */
  label?: string
  /** When true the icon is treated as decorative (aria-hidden, no aria-label). */
  decorative?: boolean
}

export const BrandIcon = memo(function BrandIcon({
  slug,
  size = 24,
  className,
  label,
  decorative = false,
}: BrandIconProps) {
  const sanitizedSvg = SANITIZED_SVG_MAP.get(slug)

  if (!sanitizedSvg) {
    // Unknown slug — render a lettermark chip. Must not throw.
    return (
      <LettermarkChip slug={slug} size={size} className={className} />
    )
  }

  const ariaLabel = decorative ? undefined : (label ?? slug)

  return (
    // The outer <span> carries the accessible role and sizing.
    // dangerouslySetInnerHTML renders the DOMPurify-sanitized SVG inline so
    // `currentColor` in the SVG picks up the surrounding text colour.
    <span
      role={decorative ? undefined : 'img'}
      aria-label={ariaLabel}
      aria-hidden={decorative ? true : undefined}
      className={className}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: size,
        height: size,
        minWidth: size,
        flexShrink: 0,
      }}
      dangerouslySetInnerHTML={{ __html: sanitizedSvg }}
    />
  )
})
