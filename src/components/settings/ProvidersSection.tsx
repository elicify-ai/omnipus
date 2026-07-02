// ProvidersSection.tsx — Provider UX fixes (supersedes the ADR-031 Track 1
// roster/grouping/terminology choices per docs/internal/specs/provider-ux-fixes-plan.md).
//
// Corrected domain model: a provider is a global, single-instance API config.
// There is exactly ONE config per catalog entry — no "instances", no
// workspace/agent binding, no "Add another". The only real variant axes are
// plan (pay-as-you-go vs Coding Plan) and region (intl/china/us). Wire
// (OpenAI- vs Anthropic-compatible) is NOT a separate provider — one
// account/key exposes both base URLs; it is an internal config detail
// surfaced only as an Endpoint-format toggle inside the config Sheet.
//
// Implements:
//   FIX-1  no per-company "Add another…" control
//   FIX-2  company group header only when ≥2 configured variants; else a flat row
//   FIX-3  configured-only list; empty state + always-visible "Connect a provider"
//          opens an on-demand picker Sheet (search + catalog grouped by company,
//          excluding already-configured entries)
//   FIX-4  real terminology — "Pay-as-you-go API" / "Coding Plan", no "Standard API"
//   FIX-5  Endpoint-format toggle (OpenAI-compatible / Anthropic-compatible) for
//          dual-wire catalog entries; muted "Anthropic endpoint" chip on the row
//   FIX-6  migration: alias / anthropic_id → canonical, self-hosted→group, unknown→generic
//   FR-009 Sheet slide-out for config AND connect (no inline expand)
//   FR-013 <BrandIcon> + lettermark fallback
//   FR-014 <BrandDisclaimer> wherever marks appear

import { useState, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  CheckCircle,
  XCircle,
  Eye,
  EyeSlash,
  ArrowCounterClockwise,
  Plus,
  X,
  CaretRight,
  Globe,
  MagnifyingGlass,
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from '@/components/ui/sheet'
import { BrandIcon } from '@/components/ui/brand-icon'
import { BrandDisclaimer } from '@/components/ui/brand-disclaimer'
import {
  fetchProviders,
  configureProvider,
  refreshProviderModels,
  testProvider,
  isApiError,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { PROVIDER_HINTS } from '@/lib/constants'
import { providerCatalogMode } from '@/lib/agents/providerCatalog'
import { ReAuthDialog } from './ReAuthDialog'
import { ProviderValidationBanner } from '@/components/providers/ProviderValidationBanner'
import { PROVIDER_CATALOG } from '@/lib/generated/providerCatalog'
import { resolveCatalogEntry } from '@/lib/providerMigration'
import type { ProviderValidation, Provider } from '@/lib/api/generated/openapi-types'
import type { ProviderCatalogEntry } from '@/lib/api/generated/openapi-types'

// A pending provider edit captured before the re-auth prompt; replayed once the
// consent token is minted. `key` carries an API-key change (empty string = no
// key change); `models` carries a manual model-slug catalogue replacement
// (undefined = leave the catalogue unchanged).
type PendingProviderChange = {
  id: string
  key: string
  models?: string[]
}

// The item shown in the Sheet — either an existing configured provider (configure
// mode) or a catalog entry for first-time setup (connect mode).
type SheetTarget =
  | { mode: 'configure'; provider: Provider; entry?: ProviderCatalogEntry }
  | { mode: 'connect'; entry: ProviderCatalogEntry }

// Which endpoint protocol a dual-wire catalog entry gets configured under
// (FIX-5). 'openai' → entry.id (default). 'anthropic' → entry.anthropic_id.
type EndpointFormat = 'openai' | 'anthropic'

// Map a plan value to a human-readable access type label (FIX-4).
function planLabel(plan: 'standard-api' | 'coding-plan'): string {
  if (plan === 'coding-plan') return 'Coding Plan'
  return 'Pay-as-you-go API'
}

// Map a region value to a human-readable string.
function regionLabel(region?: 'intl' | 'china' | 'us'): string {
  if (region === 'intl') return 'International'
  if (region === 'china') return 'China'
  if (region === 'us') return 'US'
  return ''
}

// Derive the variant row title from the catalog entry, used ONLY inside a
// grouped company block (company name is already in the group header there).
// Format: "<Plan> · <Region>" (region omitted when absent).
function variantRowTitle(entry: ProviderCatalogEntry): string {
  const plan = planLabel(entry.plan)
  const region = regionLabel(entry.region)
  return region ? `${plan} · ${region}` : plan
}

// Group configured providers by company using the migration resolver.
interface ProviderGroupItem {
  provider: Provider
  entry?: ProviderCatalogEntry
  viaAnthropicId?: boolean
}

interface ProviderGroup {
  group: string
  logoSlug?: string
  items: ProviderGroupItem[]
}

function groupProviders(providers: Provider[]): ProviderGroup[] {
  const order: string[] = []
  const map = new Map<string, ProviderGroup>()

  for (const provider of providers) {
    const { entry, group, viaAnthropicId } = resolveCatalogEntry(provider.id)
    if (!map.has(group)) {
      order.push(group)
      map.set(group, {
        group,
        logoSlug: entry?.logoSlug,
        items: [],
      })
    }
    map.get(group)!.items.push({ provider, entry, viaAnthropicId })
  }

  return order.map((g) => map.get(g)!)
}

// The set of catalog entry ids that are already configured — an id is
// "configured" if ANY fetched provider.id resolves to that entry (via
// resolveCatalogEntry, including alias / anthropic_id matches). Used to
// exclude entries from the picker (FIX-3).
function configuredEntryIds(providers: Provider[]): Set<string> {
  const ids = new Set<string>()
  for (const p of providers) {
    const { entry } = resolveCatalogEntry(p.id)
    if (entry) ids.add(entry.id)
  }
  return ids
}

// Catalog entries grouped by company, excluding already-configured ids and
// filtered by a free-text search (company name, label, or alias).
interface CatalogGroup {
  company: string
  logoSlug: string
  entries: ProviderCatalogEntry[]
}

function buildCatalogGroups(excludeIds: Set<string>, query: string): CatalogGroup[] {
  const q = query.trim().toLowerCase()
  const order: string[] = []
  const map = new Map<string, CatalogGroup>()

  for (const entry of PROVIDER_CATALOG) {
    if (excludeIds.has(entry.id)) continue
    if (q) {
      const haystack = [entry.company, entry.label, ...(entry.aliases ?? [])]
        .join(' ')
        .toLowerCase()
      if (!haystack.includes(q)) continue
    }
    if (!map.has(entry.company)) {
      order.push(entry.company)
      map.set(entry.company, {
        company: entry.company,
        logoSlug: entry.logoSlug,
        entries: [],
      })
    }
    map.get(entry.company)!.entries.push(entry)
  }

  return order.map((c) => map.get(c)!)
}

// ---------------------------------------------------------------------------
// Sub-component: Endpoint-format toggle (FIX-5)
// ---------------------------------------------------------------------------

function EndpointFormatToggle({
  entry,
  value,
  onChange,
}: {
  entry: ProviderCatalogEntry
  value: EndpointFormat
  onChange: (v: EndpointFormat) => void
}) {
  if (!entry.anthropic_id) return null

  const optionClass = (active: boolean) =>
    'flex-1 rounded-md border px-2.5 py-1.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] ' +
    (active
      ? 'border-[var(--color-accent)] bg-[var(--color-accent)]/10 text-[var(--color-accent)]'
      : 'border-[var(--color-border)] text-[var(--color-secondary)] hover:border-[var(--color-muted)]')

  return (
    <div data-testid={`endpoint-format-toggle-${entry.id}`}>
      <label className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">
        Endpoint format
      </label>
      <div className="flex gap-1.5" role="radiogroup" aria-label="Endpoint format">
        <button
          type="button"
          role="radio"
          aria-checked={value === 'openai'}
          onClick={() => onChange('openai')}
          data-testid={`endpoint-format-openai-${entry.id}`}
          className={optionClass(value === 'openai')}
        >
          OpenAI-compatible (default)
        </button>
        <button
          type="button"
          role="radio"
          aria-checked={value === 'anthropic'}
          onClick={() => onChange('anthropic')}
          data-testid={`endpoint-format-anthropic-${entry.id}`}
          className={optionClass(value === 'anthropic')}
        >
          Anthropic-compatible
        </button>
      </div>
      <p className="text-xs text-[var(--color-muted)] mt-1.5">
        Same account and API key; choose the endpoint your tools expect. Anthropic-compatible
        suits Claude-Code-style clients.
      </p>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Sub-component: Provider config Sheet (configure OR connect)
// ---------------------------------------------------------------------------

interface ProviderConfigSheetProps {
  target: SheetTarget | null
  open: boolean
  onOpenChange: (open: boolean) => void
  apiKeys: Record<string, string>
  setApiKeys: React.Dispatch<React.SetStateAction<Record<string, string>>>
  showKey: Record<string, boolean>
  setShowKey: React.Dispatch<React.SetStateAction<Record<string, boolean>>>
  draftModels: Record<string, string[]>
  setDraftModels: React.Dispatch<React.SetStateAction<Record<string, string[]>>>
  newModel: Record<string, string>
  setNewModel: React.Dispatch<React.SetStateAction<Record<string, string>>>
  saveValidation: Record<string, ProviderValidation | undefined>
  setSaveValidation: React.Dispatch<React.SetStateAction<Record<string, ProviderValidation | undefined>>>
  endpointFormats: Record<string, EndpointFormat>
  setEndpointFormats: React.Dispatch<React.SetStateAction<Record<string, EndpointFormat>>>
  isSaving: boolean
  requestChange: (id: string, key: string, models?: string[]) => void
  refreshing: Record<string, boolean>
  handleRefreshModels: (id: string) => void
  testing: Record<string, boolean>
  handleTest: (id: string) => void
}

function ProviderConfigSheet({
  target,
  open,
  onOpenChange,
  apiKeys,
  setApiKeys,
  showKey,
  setShowKey,
  draftModels,
  setDraftModels,
  newModel,
  setNewModel,
  saveValidation,
  setSaveValidation,
  endpointFormats,
  setEndpointFormats,
  isSaving,
  requestChange,
  refreshing,
  handleRefreshModels,
  testing,
  handleTest,
}: ProviderConfigSheetProps) {
  if (!target) return null

  // Determine the provider id and entry in both modes.
  const providerId = target.mode === 'configure' ? target.provider.id : target.entry.id
  const entry = target.mode === 'configure' ? target.entry : target.entry

  // For the provider object in configure mode.
  const provider = target.mode === 'configure' ? target.provider : null

  const catalogMode = provider ? providerCatalogMode(provider) : 'live'
  const hint = PROVIDER_HINTS[providerId] ?? 'Enter your API key'

  const sheetTitle = entry ? entry.label : (provider?.display_name ?? provider?.name ?? providerId)
  const sheetDescription =
    target.mode === 'connect'
      ? 'Enter your API key to connect this provider.'
      : 'Update the API key for this provider.'

  // Endpoint-format toggle (FIX-5): only meaningful when the entry has a
  // sibling anthropic_id. Default selection: 'anthropic' if this provider is
  // already configured under the anthropic_id (edit mode), else 'openai'.
  const configuredViaAnthropic =
    target.mode === 'configure' && !!entry?.anthropic_id && entry.anthropic_id === target.provider.id
  const defaultEndpointFormat: EndpointFormat = configuredViaAnthropic ? 'anthropic' : 'openai'
  const endpointFormat = endpointFormats[providerId] ?? defaultEndpointFormat
  const resolvedSubmitId =
    entry?.anthropic_id
      ? (endpointFormat === 'anthropic' ? entry.anthropic_id : entry.id)
      : providerId

  const handleClose = () => {
    onOpenChange(false)
    setSaveValidation((prev) => ({ ...prev, [providerId]: undefined }))
    setDraftModels((prev) => {
      const { [providerId]: _drop, ...rest } = prev
      return rest
    })
    setNewModel((prev) => ({ ...prev, [providerId]: '' }))
  }

  const canSave = (() => {
    const keyChanged = !!(apiKeys[providerId]?.trim())
    if (catalogMode !== 'manual') return keyChanged
    const modelsChanged = draftModels[providerId] !== undefined
    return keyChanged || modelsChanged
  })()

  return (
    <Sheet open={open} onOpenChange={(o) => { if (!o) handleClose() }}>
      <SheetContent side="right" widthClass="w-[90vw] sm:max-w-lg" data-testid="provider-config-sheet">
        <SheetHeader className="mb-6">
          <div className="flex items-center gap-3">
            {entry && (
              <BrandIcon
                slug={entry.logoSlug}
                size={28}
                decorative
              />
            )}
            <div>
              <SheetTitle>{sheetTitle}</SheetTitle>
              <SheetDescription>{sheetDescription}</SheetDescription>
            </div>
          </div>
        </SheetHeader>

        <div className="space-y-5 overflow-y-auto pr-1">
          {/* View-only variant info — Plan/Region/Endpoint. Wire is a config
              detail, not a display row (FIX-5) — see the Endpoint-format
              toggle below for dual-wire entries. */}
          {entry && (
            <div
              className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-4 py-3 space-y-2"
              data-testid="variant-info"
            >
              <div className="flex flex-wrap gap-x-6 gap-y-1.5 text-xs">
                <div>
                  <span className="text-[var(--color-muted)] mr-1.5">Plan</span>
                  <span
                    className="text-[var(--color-secondary)] font-medium"
                    data-testid="variant-plan"
                  >
                    {planLabel(entry.plan)}
                  </span>
                </div>
                {entry.region && (
                  <div>
                    <span className="text-[var(--color-muted)] mr-1.5">Region</span>
                    <span
                      className="text-[var(--color-secondary)] font-medium"
                      data-testid="variant-region"
                    >
                      {regionLabel(entry.region)}
                    </span>
                  </div>
                )}
                <div className="w-full">
                  <span className="text-[var(--color-muted)] mr-1.5">Endpoint</span>
                  <span
                    className="text-[var(--color-secondary)] font-mono text-[11px]"
                    data-testid="variant-endpoint"
                  >
                    {entry.endpointHint}
                  </span>
                </div>
              </div>
            </div>
          )}

          {/* Endpoint-format toggle — only for dual-wire catalog entries (FIX-5) */}
          {entry && (
            <EndpointFormatToggle
              entry={entry}
              value={endpointFormat}
              onChange={(v) => setEndpointFormats((prev) => ({ ...prev, [providerId]: v }))}
            />
          )}

          {/* API Key input */}
          <div>
            <label className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">
              API Key
            </label>
            <div className="relative">
              <Input
                type={showKey[providerId] ? 'text' : 'password'}
                value={apiKeys[providerId] ?? ''}
                onChange={(e) =>
                  setApiKeys((prev) => ({ ...prev, [providerId]: e.target.value }))
                }
                placeholder={hint}
                className="pr-9 font-mono text-xs"
                autoComplete="off"
                data-testid={`api-key-input-${providerId}`}
              />
              <button
                type="button"
                onClick={() =>
                  setShowKey((prev) => ({ ...prev, [providerId]: !prev[providerId] }))
                }
                className="absolute right-2.5 top-1/2 -translate-y-1/2 text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
                aria-label={showKey[providerId] ? 'Hide API key' : 'Show API key'}
              >
                {showKey[providerId] ? <EyeSlash size={14} /> : <Eye size={14} />}
              </button>
            </div>
          </div>

          {/* Manual model-slug editor — only for configure mode on manual providers */}
          {provider && catalogMode === 'manual' && (() => {
            const models = draftModels[providerId] ?? provider.models ?? []
            const pendingSlug = (newModel[providerId] ?? '').trim()
            const addSlug = () => {
              if (!pendingSlug || models.includes(pendingSlug)) return
              setDraftModels((prev) => ({
                ...prev,
                [providerId]: [...models, pendingSlug],
              }))
              setNewModel((prev) => ({ ...prev, [providerId]: '' }))
            }
            const removeSlug = (slug: string) => {
              setDraftModels((prev) => ({
                ...prev,
                [providerId]: models.filter((m) => m !== slug),
              }))
            }
            return (
              <div>
                <label className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">
                  Models
                </label>
                <p className="text-xs text-[var(--color-muted)] mb-2">
                  This provider has no live model list — add the model slugs you want available in the picker.
                </p>
                {models.length > 0 ? (
                  <ul className="flex flex-wrap gap-1.5 mb-2" data-testid={`model-list-${providerId}`}>
                    {models.map((slug) => (
                      <li key={slug}>
                        <Badge variant="muted" className="gap-1 font-mono">
                          {slug}
                          <button
                            type="button"
                            onClick={() => removeSlug(slug)}
                            aria-label={`Remove ${slug}`}
                            data-testid={`remove-model-${providerId}-${slug}`}
                            className="text-[var(--color-muted)] hover:text-[var(--color-error)]"
                          >
                            <X size={10} weight="bold" />
                          </button>
                        </Badge>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="text-xs text-[var(--color-muted)] mb-2 italic">
                    No models added yet.
                  </p>
                )}
                <div className="flex gap-2">
                  <Input
                    value={newModel[providerId] ?? ''}
                    onChange={(e) =>
                      setNewModel((prev) => ({ ...prev, [providerId]: e.target.value }))
                    }
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        e.preventDefault()
                        addSlug()
                      }
                    }}
                    placeholder="e.g. llama-3.1-70b"
                    className="font-mono text-xs"
                    data-testid={`add-model-input-${providerId}`}
                  />
                  <Button
                    variant="outline"
                    size="sm"
                    type="button"
                    onClick={addSlug}
                    disabled={!pendingSlug || models.includes(pendingSlug)}
                    data-testid={`add-model-${providerId}`}
                  >
                    <Plus size={11} /> Add
                  </Button>
                </div>
              </div>
            )
          })()}

          {/* Save-validation banner */}
          {saveValidation[providerId] && (
            <ProviderValidationBanner
              validation={saveValidation[providerId]}
              data-testid={`save-validation-banner-${providerId}`}
            />
          )}

          {/* Footer actions */}
          <div className="flex justify-between gap-2 pt-2">
            <div className="flex gap-2">
              {provider && catalogMode === 'live' && (
                <button
                  type="button"
                  onClick={() => handleRefreshModels(providerId)}
                  disabled={refreshing[providerId]}
                  title="Re-fetch the live model list from the provider"
                  data-testid={`refresh-models-${providerId}`}
                  className="text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors disabled:opacity-50"
                >
                  {refreshing[providerId] ? (
                    <ArrowCounterClockwise size={12} className="animate-spin inline" />
                  ) : 'Refresh models'}
                </button>
              )}
              {provider && provider.status === 'connected' && (
                <button
                  type="button"
                  onClick={() => handleTest(providerId)}
                  disabled={testing[providerId]}
                  title="Re-test the connection"
                  className="text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors disabled:opacity-50"
                >
                  {testing[providerId] ? (
                    <ArrowCounterClockwise size={12} className="animate-spin inline" />
                  ) : 'Test'}
                </button>
              )}
            </div>
            <div className="flex gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={handleClose}
              >
                Cancel
              </Button>
              <Button
                size="sm"
                onClick={() => {
                  const key = (apiKeys[providerId] ?? '').trim()
                  const models =
                    catalogMode === 'manual' && provider
                      ? (draftModels[providerId] ?? provider.models ?? [])
                      : undefined
                  requestChange(resolvedSubmitId, key, models)
                }}
                disabled={isSaving || !canSave}
                data-testid={`save-provider-${providerId}`}
              >
                {target.mode === 'connect' ? 'Connect' : (catalogMode === 'manual' ? 'Save' : 'Save & Connect')}
              </Button>
            </div>
          </div>
        </div>

        {/* Disclaimer */}
        {entry && (
          <div className="mt-6 pt-4 border-t border-[var(--color-border)]">
            <BrandDisclaimer />
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}

// ---------------------------------------------------------------------------
// Sub-component: Provider picker Sheet (FIX-3) — search + catalog grouped by
// company, excluding already-configured entries. Selecting an entry hands off
// to the connect form (ProviderConfigSheet, mode: connect).
// ---------------------------------------------------------------------------

interface ProviderPickerSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  query: string
  onQueryChange: (query: string) => void
  groups: CatalogGroup[]
  allConfigured: boolean
  onSelect: (entry: ProviderCatalogEntry) => void
}

function ProviderPickerSheet({
  open,
  onOpenChange,
  query,
  onQueryChange,
  groups,
  allConfigured,
  onSelect,
}: ProviderPickerSheetProps) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" widthClass="w-[90vw] sm:max-w-lg" data-testid="provider-picker-sheet">
        <SheetHeader className="mb-4">
          <SheetTitle>Connect a provider</SheetTitle>
          <SheetDescription>Choose a provider to connect an API key.</SheetDescription>
        </SheetHeader>

        <div className="space-y-4 overflow-y-auto pr-1">
          <div className="relative">
            <MagnifyingGlass
              size={13}
              className="absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-[var(--color-muted)]"
            />
            <Input
              type="search"
              value={query}
              onChange={(e) => onQueryChange(e.target.value)}
              placeholder="Search providers…"
              className="pl-8 text-sm h-8"
              aria-label="Search providers"
              data-testid="picker-search-input"
            />
          </div>

          {groups.length === 0 ? (
            <p className="text-sm text-[var(--color-muted)] text-center py-6">
              {allConfigured
                ? 'All available providers are already configured.'
                : `No providers match "${query}".`}
            </p>
          ) : (
            groups.map((group) => (
              <div key={group.company} data-testid={`picker-group-${group.company}`}>
                <div className="flex items-center gap-2 mb-1.5">
                  <BrandIcon slug={group.logoSlug} size={16} decorative />
                  <span className="text-xs font-semibold text-[var(--color-secondary)] uppercase tracking-wide">
                    {group.company}
                  </span>
                </div>
                <div className="space-y-1">
                  {group.entries.map((entry) => (
                    <button
                      key={entry.id}
                      type="button"
                      onClick={() => onSelect(entry)}
                      className="w-full flex items-center justify-between gap-2 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 text-left transition-colors hover:border-[var(--color-accent)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]"
                      data-testid={`picker-entry-${entry.id}`}
                    >
                      <span className="min-w-0">
                        <span className="block text-sm font-medium text-[var(--color-secondary)] truncate">
                          {entry.label}
                        </span>
                        <span className="block text-xs text-[var(--color-muted)] truncate">
                          {entry.subtitle}
                        </span>
                      </span>
                      <CaretRight size={12} className="shrink-0 text-[var(--color-muted)]" aria-hidden />
                    </button>
                  ))}
                </div>
              </div>
            ))
          )}

          <BrandDisclaimer className="mt-2" />
        </div>
      </SheetContent>
    </Sheet>
  )
}

// ---------------------------------------------------------------------------
// Sub-component: a single configured-provider row (flat or inside a group)
// ---------------------------------------------------------------------------

function ProviderRow({
  provider,
  entry,
  viaAnthropicId,
  title,
  showIcon,
  onConfigure,
  testValidation,
}: {
  provider: Provider
  entry?: ProviderCatalogEntry
  viaAnthropicId?: boolean
  title: string
  showIcon: boolean
  onConfigure: () => void
  testValidation?: ProviderValidation
}) {
  const connected = provider.status === 'connected'
  const catalogMode = providerCatalogMode(provider)
  const subtitle = entry?.subtitle

  return (
    <div
      className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
      data-testid={`provider-row-${provider.id}`}
    >
      {/* Test-validation warning banner */}
      {testValidation && (
        <ProviderValidationBanner
          validation={testValidation}
          data-testid={`test-validation-banner-${provider.id}`}
        />
      )}

      <div className="flex items-center gap-3 px-4 py-3">
        {showIcon && (
          entry ? (
            <BrandIcon slug={entry.logoSlug} size={22} decorative className="shrink-0" />
          ) : (
            <Globe size={22} className="text-[var(--color-muted)] shrink-0" aria-hidden="true" />
          )
        )}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span
              className="text-sm font-medium text-[var(--color-secondary)]"
              data-testid={`provider-row-title-${provider.id}`}
            >
              {title}
            </span>
            {/* Anthropic-endpoint chip (FIX-5) — only when configured under anthropic_id */}
            {viaAnthropicId && (
              <Badge
                variant="muted"
                className="font-normal text-[10px]"
                data-testid={`anthropic-endpoint-chip-${provider.id}`}
              >
                Anthropic endpoint
              </Badge>
            )}
            {connected ? (
              <Badge data-testid={`connected-badge-${provider.id}`} variant="success" className="gap-1">
                <CheckCircle size={10} weight="fill" /> Connected
              </Badge>
            ) : provider.status === 'error' ? (
              <Badge
                variant="error"
                className="gap-1"
                data-testid={`error-badge-${provider.id}`}
              >
                <XCircle size={10} weight="fill" /> Error
              </Badge>
            ) : (
              <Badge variant="muted" data-testid={`disconnected-badge-${provider.id}`}>
                Not configured
              </Badge>
            )}
            {connected && (
              <Badge variant="muted" className="font-normal">
                {catalogMode === 'live' ? 'Live model list' : 'Manual models'}
              </Badge>
            )}
          </div>
          {subtitle && (
            <p className="text-xs text-[var(--color-muted)] mt-0.5">
              {subtitle}
            </p>
          )}
          {provider.models && provider.models.length > 0 && (
            <p className="text-xs text-[var(--color-muted)] mt-0.5 font-mono">
              {provider.models.slice(0, 3).join(', ')}{provider.models.length > 3 ? ` +${provider.models.length - 3}` : ''}
            </p>
          )}
          {provider.error && (
            <p className="text-xs text-[var(--color-error)] mt-0.5">{provider.error}</p>
          )}
        </div>

        <div className="flex items-center gap-2 shrink-0">
          <Button
            size="sm"
            onClick={onConfigure}
            className="h-7 px-3 text-xs"
            data-testid={`configure-btn-${provider.id}`}
          >
            {connected ? 'Edit' : (
              <><CaretRight size={11} /> Configure</>
            )}
          </Button>
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Main ProvidersSection
// ---------------------------------------------------------------------------

export function ProvidersSection() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  // Sheet state
  const [sheetTarget, setSheetTarget] = useState<SheetTarget | null>(null)
  const [sheetOpen, setSheetOpen] = useState(false)

  // Picker Sheet state (FIX-3)
  const [pickerOpen, setPickerOpen] = useState(false)
  const [pickerQuery, setPickerQuery] = useState('')

  const [apiKeys, setApiKeys] = useState<Record<string, string>>({})
  const [showKey, setShowKey] = useState<Record<string, boolean>>({})
  const [testing, setTesting] = useState<Record<string, boolean>>({})
  const [refreshing, setRefreshing] = useState<Record<string, boolean>>({})
  const [saveValidation, setSaveValidation] = useState<Record<string, ProviderValidation | undefined>>({})
  const [testValidation, setTestValidation] = useState<Record<string, ProviderValidation | undefined>>({})
  const [draftModels, setDraftModels] = useState<Record<string, string[]>>({})
  const [newModel, setNewModel] = useState<Record<string, string>>({})
  const [endpointFormats, setEndpointFormats] = useState<Record<string, EndpointFormat>>({})

  const [pending, setPending] = useState<PendingProviderChange | null>(null)
  const [reauthOpen, setReauthOpen] = useState(false)

  const { data: providers = [], isLoading, isError: providersError } = useQuery({
    queryKey: ['providers'],
    queryFn: fetchProviders,
  })

  const { mutate: applyChange, isPending: isSaving } = useMutation({
    mutationFn: ({ id, key, token, models }: { id: string; key: string; token: string; models?: string[] }) =>
      configureProvider(id, key === '' ? undefined : key, undefined, undefined, token, models),
    onSuccess: (provider, { id }) => {
      queryClient.invalidateQueries({ queryKey: ['providers'] })
      const validation = provider.validation
      if (validation?.outcome === 'invalid_key') {
        // invalid_key is the ONE outcome the contract says blocks a usable
        // provider. Do NOT report a green success — surface an error and keep
        // the sheet open with the banner so the user can correct the key.
        setSaveValidation((prev) => ({ ...prev, [id]: validation }))
        addToast({
          message: validation.message ?? 'Key rejected — the provider was saved but will not work until you fix the key',
          variant: 'error',
        })
      } else if (validation && validation.outcome !== 'valid') {
        // Non-blocking outcomes (no_credit / unreachable / restricted): saved
        // successfully; keep the sheet open so the amber warning banner is
        // visible. (Unchanged from prior behavior.)
        setSaveValidation((prev) => ({ ...prev, [id]: validation }))
        addToast({ message: 'Provider saved', variant: 'success' })
      } else {
        setSaveValidation((prev) => ({ ...prev, [id]: undefined }))
        addToast({ message: 'Provider saved', variant: 'success' })
        setSheetOpen(false)
      }
      setPending(null)
      setApiKeys((prev) => ({ ...prev, [id]: '' }))
    },
    onError: (err: Error) => {
      addToast({ message: isApiError(err) ? err.userMessage : err.message, variant: 'error' })
      setPending(null)
    },
  })

  const requestChange = (id: string, key: string, models?: string[]) => {
    setSaveValidation((prev) => ({ ...prev, [id]: undefined }))
    setPending({ id, key, models })
    setReauthOpen(true)
  }

  const onReAuthConfirmed = (token: string) => {
    if (!pending) return
    applyChange({ id: pending.id, key: pending.key, token, models: pending.models })
  }

  const handleRefreshModels = async (id: string) => {
    setRefreshing((prev) => ({ ...prev, [id]: true }))
    try {
      const updated = await refreshProviderModels(id)
      queryClient.invalidateQueries({ queryKey: ['providers'] })
      if (updated.warning) {
        addToast({ message: updated.warning, variant: 'error' })
      } else {
        addToast({ message: `Model list refreshed (${updated.models?.length ?? 0})`, variant: 'success' })
      }
    } catch (err) {
      addToast({ message: isApiError(err) ? err.userMessage : (err as Error).message, variant: 'error' })
    } finally {
      setRefreshing((prev) => ({ ...prev, [id]: false }))
    }
  }

  const handleTest = async (id: string) => {
    setTesting((prev) => ({ ...prev, [id]: true }))
    setTestValidation((prev) => ({ ...prev, [id]: undefined }))
    try {
      const result = await testProvider(id)
      if (result.success) {
        if (result.validation && result.validation.outcome !== 'valid') {
          setTestValidation((prev) => ({ ...prev, [id]: result.validation }))
        }
        addToast({ message: 'Connection successful', variant: 'success' })
        queryClient.invalidateQueries({ queryKey: ['providers'] })
      } else {
        addToast({ message: result.error ?? 'Connection failed', variant: 'error' })
      }
    } catch (err) {
      addToast({ message: (err as Error).message, variant: 'error' })
    } finally {
      setTesting((prev) => ({ ...prev, [id]: false }))
    }
  }

  const openConfigureSheet = (provider: Provider) => {
    const { entry } = resolveCatalogEntry(provider.id)
    setSheetTarget({ mode: 'configure', provider, entry })
    setSheetOpen(true)
  }

  const openConnectSheet = (entry: ProviderCatalogEntry) => {
    setSheetTarget({ mode: 'connect', entry })
    setSheetOpen(true)
  }

  const openPicker = () => {
    setPickerQuery('')
    setPickerOpen(true)
  }

  const groups = groupProviders(providers)
  const hasConfigured = providers.length > 0

  const excludeIds = useMemo(() => configuredEntryIds(providers), [providers])
  const catalogGroups = useMemo(
    () => buildCatalogGroups(excludeIds, pickerQuery),
    [excludeIds, pickerQuery],
  )
  const allConfigured = excludeIds.size >= PROVIDER_CATALOG.length

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h2 className="font-headline font-bold text-base text-[var(--color-secondary)]">Providers</h2>
          <p className="text-xs text-[var(--color-muted)] mt-0.5">
            API keys are stored encrypted in credentials.json — never in config.json.
          </p>
        </div>
        {/* Connect a provider — always available once at least one provider is
            configured (FIX-3: there was previously no way to add a provider
            after the first). */}
        {hasConfigured && (
          <Button
            size="sm"
            onClick={openPicker}
            className="h-8 px-3 text-xs shrink-0 gap-1.5"
            data-testid="connect-provider-btn"
          >
            <Plus size={12} weight="bold" /> Connect a provider
          </Button>
        )}
      </div>

      {providersError && (
        <p className="text-sm text-red-400">Failed to load providers. Please try again.</p>
      )}

      {isLoading ? (
        <div className="space-y-2">
          {[1, 2, 3].map((i) => (
            <div
              key={i}
              className="h-14 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] animate-pulse"
            />
          ))}
        </div>
      ) : hasConfigured ? (
        /* ── Configured-only list, grouped only when a company has ≥2
             configured variants (FIX-2); a single configured provider is a
             flat row with its own BrandIcon + entry.label. ── */
        <div className="space-y-4">
          {groups.map((group) => {
            if (group.items.length === 1) {
              const { provider, entry, viaAnthropicId } = group.items[0]
              const title = entry ? entry.label : (provider.display_name ?? provider.name ?? provider.id)
              return (
                <ProviderRow
                  key={provider.id}
                  provider={provider}
                  entry={entry}
                  viaAnthropicId={viaAnthropicId}
                  title={title}
                  showIcon
                  onConfigure={() => openConfigureSheet(provider)}
                  testValidation={testValidation[provider.id]}
                />
              )
            }

            return (
              <div key={group.group} data-testid={`provider-group-${group.group}`}>
                {/* Company group header — only rendered here, when ≥2 variants
                    are configured (FIX-1: no "Add another…" control). */}
                <div className="flex items-center gap-2 mb-1.5">
                  {group.logoSlug ? (
                    <BrandIcon
                      slug={group.logoSlug}
                      size={18}
                      decorative
                    />
                  ) : (
                    <Globe size={18} className="text-[var(--color-muted)]" aria-hidden="true" />
                  )}
                  <span
                    className="text-xs font-semibold text-[var(--color-secondary)] uppercase tracking-wide"
                    data-testid={`group-header-${group.group}`}
                  >
                    {group.group}
                  </span>
                </div>

                <div className="space-y-1.5">
                  {group.items.map(({ provider, entry, viaAnthropicId }) => (
                    <ProviderRow
                      key={provider.id}
                      provider={provider}
                      entry={entry}
                      viaAnthropicId={viaAnthropicId}
                      title={entry ? variantRowTitle(entry) : (provider.display_name ?? provider.name ?? provider.id)}
                      showIcon={false}
                      onConfigure={() => openConfigureSheet(provider)}
                      testValidation={testValidation[provider.id]}
                    />
                  ))}
                </div>
              </div>
            )
          })}

          {/* Trademark disclaimer whenever brand marks are shown */}
          <BrandDisclaimer className="mt-2" />
        </div>
      ) : (
        /* ── Empty state (FIX-3) — no default-visible roster; a compact
             message + one primary "Connect a provider" CTA. ── */
        <div
          className="rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-8 text-center space-y-3"
          data-testid="providers-empty-state"
        >
          <p className="text-sm text-[var(--color-muted)]">No providers configured yet.</p>
          <Button
            onClick={openPicker}
            className="gap-1.5"
            data-testid="connect-provider-btn"
          >
            <Plus size={13} weight="bold" /> Connect a provider
          </Button>
        </div>
      )}

      {/* Provider picker Sheet (FIX-3) */}
      <ProviderPickerSheet
        open={pickerOpen}
        onOpenChange={setPickerOpen}
        query={pickerQuery}
        onQueryChange={setPickerQuery}
        groups={catalogGroups}
        allConfigured={allConfigured}
        onSelect={(entry) => {
          setPickerOpen(false)
          openConnectSheet(entry)
        }}
      />

      {/* Provider config/connect Sheet */}
      <ProviderConfigSheet
        target={sheetTarget}
        open={sheetOpen}
        onOpenChange={(o) => {
          setSheetOpen(o)
          if (!o) {
            // Clean up on close
            if (sheetTarget) {
              const id = sheetTarget.mode === 'configure' ? sheetTarget.provider.id : sheetTarget.entry.id
              setSaveValidation((prev) => ({ ...prev, [id]: undefined }))
            }
          }
        }}
        apiKeys={apiKeys}
        setApiKeys={setApiKeys}
        showKey={showKey}
        setShowKey={setShowKey}
        draftModels={draftModels}
        setDraftModels={setDraftModels}
        newModel={newModel}
        setNewModel={setNewModel}
        saveValidation={saveValidation}
        setSaveValidation={setSaveValidation}
        endpointFormats={endpointFormats}
        setEndpointFormats={setEndpointFormats}
        isSaving={isSaving}
        requestChange={requestChange}
        refreshing={refreshing}
        handleRefreshModels={handleRefreshModels}
        testing={testing}
        handleTest={handleTest}
      />

      <ReAuthDialog
        open={reauthOpen}
        onOpenChange={(o) => {
          setReauthOpen(o)
          if (!o) setPending(null)
        }}
        title="Confirm to update provider"
        description="Re-type your password to change this provider's API key."
        onConfirmed={onReAuthConfirmed}
      />
    </div>
  )
}
