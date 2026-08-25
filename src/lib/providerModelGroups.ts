// providerModelGroups.ts — turn the operator's CONFIGURED providers plus the
// registry-fed catalog into the `ModelCatalogGroup[]` the shared
// `ModelSelector` renders (ADR-068 FR-019 / FR-030).
//
// Two surfaces need exactly this and must agree on it: the *Default model*
// card's *Change* selector and the Remove dialog's inline *New default model*
// selector. Building it here rather than in either component is what keeps
// them from drifting — and gives T068-26/T068-27 one named helper to reuse.
//
// A configured row can be in the catalog (its `CatalogProvider.models` carry
// windows, release dates and modalities) or not (a custom endpoint or an
// endpoint-less provider whose slugs the operator typed). Both must be
// offerable, so a non-catalog row's bare slugs are wrapped as catalog models
// with DELIBERATELY empty metadata: window 0, no tool-call claim. We do not
// know those numbers, and inventing them would both mislead the reader and
// hand the model a "Recommended for chat" chip it never earned.

import type { CatalogModel, CatalogProvider, Provider, ProvidersCatalog } from '@/lib/api/generated/openapi-types'
import type { ModelCatalogGroup } from '@/components/ui/model-selector'
import { resolveCatalogEntry } from '@/lib/providerMigration'
import { catalogLabel } from '@/lib/catalogDisplay'

/** Display name for a configured row: catalog label → wire display name → id. */
export function providerDisplayName(
  provider: Provider,
  entry?: CatalogProvider,
): string {
  if (entry) return catalogLabel(entry)
  return provider.display_name ?? provider.name ?? provider.id
}

/** A bare slug the catalog knows nothing about, in `CatalogModel` clothing. */
export function slugAsCatalogModel(slug: string): CatalogModel {
  return {
    id: slug,
    name: slug,
    context_window: 0,
    max_output_tokens: 0,
    input_modalities: ['text'],
    tool_call: false,
    status: 'active',
  }
}

export interface ProviderModelGroupsInput {
  providers: readonly Provider[]
  catalog?: ProvidersCatalog | null
  /** Appended after the display name, e.g. the row's status. Omit for none. */
  annotate?: (provider: Provider) => string | undefined
}

/**
 * One group per configured provider that has at least one offerable model, in
 * the order the providers were given. A provider with no models at all is
 * dropped — the selector cannot offer anything for it, and an empty heading
 * reads as a bug.
 */
export function buildProviderModelGroups({
  providers,
  catalog,
  annotate,
}: ProviderModelGroupsInput): ModelCatalogGroup[] {
  const entries = catalog?.providers ?? []
  const groups: ModelCatalogGroup[] = []

  for (const provider of providers) {
    const { entry } = resolveCatalogEntry(entries, provider.id)
    const catalogModels = entry?.models ?? []
    const models: CatalogModel[] =
      catalogModels.length > 0 ? catalogModels : (provider.models ?? []).map(slugAsCatalogModel)
    if (models.length === 0) continue

    const name = providerDisplayName(provider, entry)
    const suffix = annotate?.(provider)
    groups.push({
      providerId: provider.id,
      providerName: suffix ? `${name} · ${suffix}` : name,
      status: provider.status,
      models,
    })
  }

  return groups
}
