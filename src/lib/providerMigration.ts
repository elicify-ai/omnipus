// providerMigration.ts — ADR-031 Track 1 migration resolver (FR-012, US-8).
//
// `resolveCatalogEntry` maps a stored provider id (possibly an alias, an
// anthropic-wire sibling id, a known self-hosted id, or an unrecognised
// string) to a display group and, where possible, a canonical
// ProviderCatalogEntry.
//
// Five cases (in priority order — catalog membership wins first):
//   1. Exact canonical id in the catalog → that entry + entry.company.
//   2. Alias match in the catalog → canonical entry + entry.company.
//   3. `anthropic_id` match (the merged Anthropic-compatible sibling protocol,
//      FIX-5) → canonical entry + entry.company + viaAnthropicId: true.
//   4. Known self-hosted id that is NOT in the catalog (litellm / vllm) →
//      "Self-hosted / Custom" (localhost endpoint, no cloud roster slot).
//   5. Unrecognised / empty → generic "Other" group, no catalog entry.
//
// NB: `ollama` is a FIRST-CLASS catalog provider ("Ollama (local)"), so it
// resolves via case 1 — it is NOT force-routed to Self-hosted / Custom (that was
// the double-grouping bug: a configured ollama would appear under a different
// group name than the roster shows it under). Only genuinely not-in-catalog
// self-hosted runtimes (litellm/vllm) use the Self-hosted / Custom fallback.
//
// NEVER crashes: all inputs including "" and garbage strings are handled.

import { PROVIDER_CATALOG } from '@/lib/generated/providerCatalog'
import type { ProviderCatalogEntry } from '@/lib/api/generated/openapi-types'

export const SELF_HOSTED_CUSTOM_GROUP = 'Self-hosted / Custom'
export const GENERIC_GROUP = 'Other'

// Known self-hosted runtimes that are NOT offered in the catalog/roster. When
// already configured they group under Self-hosted / Custom rather than "Other",
// so they aren't demoted to "unknown" (ADR-031 §7 G-4 / MAJ-004). `ollama` is
// deliberately absent — it is a catalog provider and resolves via the catalog.
const SELF_HOSTED_IDS = new Set(['litellm', 'vllm'])

export interface ResolvedCatalogEntry { // not-wire-format: local migration-resolver result (a display group + optional ProviderCatalogEntry); never serialized or crosses the gateway/SPA boundary
  /** Catalog entry if resolved; undefined for self-hosted-custom or unknown ids. */
  entry?: ProviderCatalogEntry
  /** Display group name to use in the grouped list. */
  group: string
  /**
   * True when `storedId` resolved via `entry.anthropic_id` (the merged
   * Anthropic-compatible sibling protocol) rather than the entry's primary
   * `id` or an `aliases` entry. Drives the muted "Anthropic endpoint" row
   * chip and the Endpoint-format toggle's initial selection (FIX-5).
   */
  viaAnthropicId?: boolean
}

/**
 * Resolve a stored provider id to a catalog entry and display group.
 *
 * Cases (catalog membership wins first):
 *  - Canonical catalog id (exact id match) → that entry + entry.company.
 *  - Alias id (matches entry.aliases) → canonical entry + entry.company.
 *  - `anthropic_id` match (the merged Anthropic-compatible sibling protocol)
 *    → canonical entry + entry.company + viaAnthropicId: true.
 *  - Known not-in-catalog self-hosted id (litellm/vllm) → "Self-hosted / Custom".
 *  - Empty / unrecognised → group "Other" with no entry.
 */
export function resolveCatalogEntry(storedId: string): ResolvedCatalogEntry {
  if (!storedId) {
    return { group: GENERIC_GROUP }
  }

  // Priority 1: exact canonical id match (a catalog provider like `ollama` wins here)
  const byId = PROVIDER_CATALOG.find((e) => e.id === storedId)
  if (byId) {
    return { entry: byId, group: byId.company }
  }

  // Priority 2: alias match → canonical entry
  const byAlias = PROVIDER_CATALOG.find((e) => e.aliases?.includes(storedId))
  if (byAlias) {
    return { entry: byAlias, group: byAlias.company }
  }

  // Priority 3: anthropic_id match (dual-wire sibling, e.g. `z-ai-anthropic`
  // resolves to the merged `z-ai` entry) → canonical entry, flagged so the
  // row can show the "Anthropic endpoint" chip.
  const byAnthropicId = PROVIDER_CATALOG.find((e) => e.anthropic_id === storedId)
  if (byAnthropicId) {
    return { entry: byAnthropicId, group: byAnthropicId.company, viaAnthropicId: true }
  }

  // Priority 4: known self-hosted runtime not in the catalog → Self-hosted / Custom
  if (SELF_HOSTED_IDS.has(storedId)) {
    return { group: SELF_HOSTED_CUSTOM_GROUP }
  }

  // Fallback: unknown id
  return { group: GENERIC_GROUP }
}
