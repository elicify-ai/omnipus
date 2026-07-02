// providerMigration.ts — ADR-031 Track 1 migration resolver (FR-012, US-8).
//
// `resolveCatalogEntry` maps a stored provider id (possibly an alias, a known
// self-hosted id, or an unrecognised string) to a display group and, where
// possible, a canonical ProviderCatalogEntry.
//
// Three cases (in priority order):
//   1. Known self-hosted ids (ollama / litellm / vllm) → "Self-hosted / Custom"
//      (they have localhost endpoints and need no cloud API key roster slot).
//   2. Alias match → canonical catalog entry + that entry's company as group.
//   3. Unrecognised / empty → generic group with raw id, no catalog entry.
//
// NEVER crashes: all inputs including "" and garbage strings are handled.

import { PROVIDER_CATALOG } from '@/lib/generated/providerCatalog'
import type { ProviderCatalogEntry } from '@/lib/api/generated/openapi-types'

export const SELF_HOSTED_CUSTOM_GROUP = 'Self-hosted / Custom'
export const GENERIC_GROUP = 'Other'

// Ids that are treated as "self-hosted" regardless of catalog membership.
// These have localhost / local endpoints and should not appear in the cloud
// provider roster; when already configured, they group under Self-hosted / Custom.
const SELF_HOSTED_IDS = new Set(['ollama', 'litellm', 'vllm'])

export interface ResolvedCatalogEntry {
  /** Catalog entry if resolved; undefined for self-hosted-custom or unknown ids. */
  entry?: ProviderCatalogEntry
  /** Display group name to use in the grouped list. */
  group: string
}

/**
 * Resolve a stored provider id to a catalog entry and display group.
 *
 * Cases:
 *  - Self-hosted ids (ollama/litellm/vllm) → group "Self-hosted / Custom", no entry.
 *  - Canonical catalog id (exact id match) → that entry + entry.company.
 *  - Alias id (matches entry.aliases) → canonical entry + entry.company.
 *  - Empty / unrecognised → group "Other" with no entry.
 */
export function resolveCatalogEntry(storedId: string): ResolvedCatalogEntry {
  if (!storedId) {
    return { group: GENERIC_GROUP }
  }

  // Priority 1: self-hosted ids → "Self-hosted / Custom"
  if (SELF_HOSTED_IDS.has(storedId)) {
    return { group: SELF_HOSTED_CUSTOM_GROUP }
  }

  // Priority 2: exact canonical id match
  const byId = PROVIDER_CATALOG.find((e) => e.id === storedId)
  if (byId) {
    return { entry: byId, group: byId.company }
  }

  // Priority 3: alias match
  const byAlias = PROVIDER_CATALOG.find((e) => e.aliases?.includes(storedId))
  if (byAlias) {
    return { entry: byAlias, group: byAlias.company }
  }

  // Fallback: unknown id
  return { group: GENERIC_GROUP }
}
