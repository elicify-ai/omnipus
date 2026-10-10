// Shared constants and utilities used across multiple components

import type { components } from '@/lib/api/generated/openapi-types'

/** Generate an unguessable ID from a CSPRNG: crypto.randomUUID() where
 *  available, else crypto.getRandomValues() (128-bit — it is not gated to
 *  secure contexts, so it covers plain HTTP). These ids serve as unguessable
 *  storage/URL keys for uploads, so there is deliberately no predictable
 *  fallback: with no secure source, generateId() throws. */
export function generateId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    const bytes = crypto.getRandomValues(new Uint8Array(16))
    return `${Date.now().toString(36)}-${Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')}`
  }
  throw new Error('generateId: no secure random source available (crypto.randomUUID / crypto.getRandomValues)')
}

/** One of the ten identity colours the contract allows (`AgentColor`). */
export type AvatarColor = components['schemas']['AgentColor']

/** Avatar color palette for agent creation and display — exactly the
 *  contract's `AgentColor` set; the server rejects any other value. */
export const AVATAR_COLORS: readonly AvatarColor[] = [
  '#3B82F6', '#38BDF8', '#22D3EE', '#818CF8', '#A78BFA',
  '#C084FC', '#E879F9', '#F472B6', '#FB923C', '#9CA3AF',
]

/**
 * Semantic names for each avatar color, indexed by hex. W6-B4 / M7: replaces
 * hex codes in aria-labels and visible labels so screen readers and
 * sighted users get a usable name (e.g. "Azure") instead of a hex
 * string (e.g. "#3B82F6").
 *
 * Wire format and state in formData / Agent.color are unchanged — the hex
 * is the source of truth. The name is presentation-only. Typed as a full
 * Record over `AvatarColor`, so a contract change to the colour set fails
 * typecheck here until the names are updated.
 */
export const AVATAR_COLORS_BY_NAME: Record<AvatarColor, string> = {
  '#3B82F6': 'Azure',
  '#38BDF8': 'Sky',
  '#22D3EE': 'Aqua',
  '#818CF8': 'Periwinkle',
  '#A78BFA': 'Violet',
  '#C084FC': 'Orchid',
  '#E879F9': 'Fuchsia',
  '#F472B6': 'Rose',
  '#FB923C': 'Tangerine',
  '#9CA3AF': 'Slate',
}

/** Get the semantic name for an avatar color hex; falls back to the hex
 *  itself if the color is not in the palette (defense for free-text). */
export function avatarColorName(hex: string | undefined): string {
  if (!hex) return 'Default'
  return (AVATAR_COLORS_BY_NAME as Record<string, string>)[hex] ?? hex
}

/** Hint text for API key input fields, keyed by canonical CatalogProvider id
 *  (the `id` of GET /providers/catalog entries, ADR-067 schema 2.0.0). */
export const PROVIDER_HINTS: Record<string, string> = {
  anthropic: 'Starts with sk-ant-...',
  openai: 'Starts with sk-...',
  groq: 'Starts with gsk_...',
  openrouter: 'Starts with sk-or-v1-...',
}
