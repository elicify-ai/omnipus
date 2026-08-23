// Shared constants and utilities used across multiple components

/** Generate a unique ID. Uses crypto.randomUUID() in secure contexts (HTTPS),
 *  falls back to a timestamp+random string for HTTP contexts. */
export function generateId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  // Fallback for non-secure contexts (HTTP)
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

/** Avatar color palette for agent creation and display. */
export const AVATAR_COLORS = ['#22C55E', '#3B82F6', '#A855F7', '#EAB308', '#F97316', '#EF4444', '#6B7280', '#D4AF37']

/**
 * Semantic names for each avatar color, indexed by hex. W6-B4 / M7: replaces
 * hex codes in aria-labels and visible labels so screen readers and
 * sighted users get a usable name (e.g. "Forge Gold") instead of a hex
 * string (e.g. "#D4AF37"). The first three match the brand palette
 * (`docs/internal/brand/brand-guidelines.md`); the rest are descriptive
 * shades picked from the Tailwind v4 / Phosphor icon palette to keep the
 * naming consistent with the rest of the design system.
 *
 * Wire format and state in formData / Agent.color are unchanged — the hex
 * is the source of truth. The name is presentation-only.
 */
export const AVATAR_COLORS_BY_NAME: Record<string, string> = {
  '#22C55E': 'Verdant',
  '#3B82F6': 'Azure',
  '#A855F7': 'Amethyst',
  '#EAB308': 'Saffron',
  '#F97316': 'Ember',
  '#EF4444': 'Crimson',
  '#6B7280': 'Slate',
  '#D4AF37': 'Forge Gold',
}

/** Get the semantic name for an avatar color hex; falls back to the hex
 *  itself if the color is not in the palette (defense for free-text). */
export function avatarColorName(hex: string | undefined): string {
  if (!hex) return 'Default'
  return AVATAR_COLORS_BY_NAME[hex] ?? hex
}

/** Hint text for API key input fields, keyed by canonical CatalogProvider id
 *  (the `id` of GET /providers/catalog entries, ADR-067 schema 2.0.0). */
export const PROVIDER_HINTS: Record<string, string> = {
  anthropic: 'Starts with sk-ant-...',
  openai: 'Starts with sk-...',
  groq: 'Starts with gsk_...',
  openrouter: 'Starts with sk-or-v1-...',
}
