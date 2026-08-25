// catalogDisplay.ts — presentation helpers over the registry-fed
// CatalogProvider (ADR-067 schema 2.0.0, served by GET /providers/catalog).
//
// The bundled ProviderCatalogEntry emission used to carry hand-authored
// display fields (label, subtitle, logoSlug, endpointHint). That file is gone
// (ADR-068 FR-037 / SC-010); the 2.0.0 document carries only data, so the SPA
// derives display strings here — one place, shared by onboarding and Settings.

import type { CatalogProvider } from '@/lib/api/generated/openapi-types'
import { planLabel, regionLabel } from '@/lib/providerLabels'

// Company → vendored brand-logo slug (src/assets/brand-logos/p_<slug>.svg).
// Anything not listed falls back to the provider id, which BrandIcon renders
// as a lettermark chip when no SVG exists for it.
const COMPANY_LOGO_SLUGS: Record<string, string> = {
  'zhipu ai': 'zhipu',
  'zhipu': 'zhipu',
  'z.ai': 'zhipu',
  'google': 'gemini',
  'google gemini': 'gemini',
  'moonshot ai': 'moonshot',
  'moonshot': 'moonshot',
  'kimi': 'kimi',
  'alibaba': 'qwen',
  'alibaba cloud': 'qwen',
  'qwen': 'qwen',
  'minimax': 'minimax',
  'mistral': 'mistral',
  'mistral ai': 'mistral',
  'deepseek': 'deepseek',
  'groq': 'groq',
  'openai': 'openai',
  'anthropic': 'anthropic',
  'openrouter': 'openrouter',
}

/** Brand-logo slug for a catalog provider (lettermark fallback via BrandIcon). */
export function catalogLogoSlug(entry: CatalogProvider): string {
  return COMPANY_LOGO_SLUGS[entry.company.trim().toLowerCase()] ?? entry.id
}

/** Display host for the provider's primary endpoint ("api.z.ai/api/paas/v4"). */
export function catalogEndpointHint(entry: CatalogProvider): string {
  return entry.api.replace(/^[a-z][a-z0-9+.-]*:\/\//i, '').replace(/\/+$/, '')
}

/** Full human-readable label: "<name> [(Region)]" — coding plans get a suffix. */
export function catalogLabel(entry: CatalogProvider): string {
  const plan = entry.plan && entry.plan !== 'standard-api' ? ` — ${planLabel(entry.plan)}` : ''
  const region = entry.region ? ` (${regionLabel(entry.region)})` : ''
  return `${entry.name}${plan}${region}`
}

/** "<billing model> · <endpoint host>" shown under the label. */
export function catalogSubtitle(entry: CatalogProvider): string {
  const billing = entry.plan === 'coding-plan' ? 'Subscription (Coding Plan)' : 'Pay-as-you-go, per token'
  return `${billing} · ${catalogEndpointHint(entry)}`
}

/** Variant row title inside a company group: "<Plan> · <Region>" (region omitted when absent). */
export function catalogVariantTitle(entry: CatalogProvider): string {
  const plan = planLabel(entry.plan)
  const region = regionLabel(entry.region)
  return region ? `${plan} · ${region}` : plan
}

// ---------------------------------------------------------------------------
// Identity — exact, greenfield (ADR-067 FR-011 / FR-030, US-11.AC2)
// ---------------------------------------------------------------------------
//
// A stored provider id is a catalog id or an operator-named custom row. There
// is no alias table, no rename ladder and no "known self-hosted" side list:
// `src/lib/providerMigration.ts` carried all three and is deleted (T067-13).
// `CatalogProvider.aliases[]` is SEARCH-ONLY (FR-030) — it must never
// participate in resolution, or the SPA would show a configured row under a
// canonical identity the gateway does not agree with.

/** Display group for a configured row whose id the catalog does not carry. */
export const UNGROUPED_PROVIDER_GROUP = 'Other'

/**
 * The catalog row for a stored provider id — EXACT id match only.
 *
 * Undefined for an operator-named custom row, for an id the served document
 * does not carry, for the empty string, and while the catalog GET is still in
 * flight (an empty array). Never throws.
 */
export function catalogEntryById(
  catalog: readonly CatalogProvider[],
  id: string,
): CatalogProvider | undefined {
  if (!id) return undefined
  return catalog.find((entry) => entry.id === id)
}

/**
 * Display group name for a configured provider row: the catalog company when
 * the id resolved, else "Other".
 */
export function catalogGroupName(entry: CatalogProvider | undefined): string {
  return entry?.company ?? UNGROUPED_PROVIDER_GROUP
}
