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
  Eye,
  EyeSlash,
  ArrowCounterClockwise,
  Plus,
  X,
  Globe,
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
  getErrorMessage,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { PROVIDER_HINTS } from '@/lib/constants'
import { PLAN_LABELS, REGION_LABELS } from '@/lib/providerLabels'
import { providerCatalogMode } from '@/lib/agents/providerCatalog'
import { ReAuthDialog } from './ReAuthDialog'
import { ProviderValidationBanner } from '@/components/providers/ProviderValidationBanner'
import { PROVIDER_CATALOG } from '@/lib/generated/providerCatalog'
import { resolveCatalogEntry } from '@/lib/providerMigration'
import { ProviderRow } from './ProviderRow'
import { ProviderPickerSheet } from './ProviderPickerSheet'
import type { CatalogGroup } from './ProviderPickerSheet'
import type { ProviderValidation, Provider } from '@/lib/api/generated/openapi-types'
import type { ProviderCatalogEntry } from '@/lib/api/generated/openapi-types'

// A pending provider edit captured before the re-auth prompt; replayed once the
// consent token is minted. `id` is the id submitted to the PUT (resolveSubmitId).
// `draftKey` is the canonical per-provider draft-state key (sheetDraftKey) —
// carried through so the mutation's success/close handlers clear the SAME key
// the Sheet read from, even when `id` diverges from it (alias / anthropic_id
// storage, or the connect-mode endpoint-format toggle). `key` carries an
// API-key change (empty string = no key change); `models` carries a manual
// model-slug catalogue replacement (undefined = leave the catalogue unchanged).
type PendingProviderChange = {
  id: string
  draftKey: string
  key: string
  models?: string[]
}

// The item shown in the Sheet — either an existing configured provider (configure
// mode) or a catalog entry for first-time setup (connect mode). `viaAnthropicId`
// (configure mode only) is threaded from resolveCatalogEntry at the point the
// Sheet was opened — the single source of truth for whether the stored
// provider.id is the entry's primary id or its anthropic_id sibling, so the
// read-only endpoint-format display doesn't need to re-derive it.
type SheetTarget =
  | { mode: 'configure'; provider: Provider; entry?: ProviderCatalogEntry; viaAnthropicId?: boolean }
  | { mode: 'connect'; entry: ProviderCatalogEntry }

// Which endpoint protocol a dual-wire catalog entry gets configured under
// (FIX-5). 'openai' → entry.id (default). 'anthropic' → entry.anthropic_id.
type EndpointFormat = 'openai' | 'anthropic'

// A catalog entry known to expose an Anthropic-compatible sibling endpoint —
// narrows `anthropic_id` from optional to required so callers (the toggle,
// the read-only configure-mode display) don't need to re-check it.
type DualWireCatalogEntry = ProviderCatalogEntry & { anthropic_id: string }

function isDualWire(entry: ProviderCatalogEntry): entry is DualWireCatalogEntry {
  return !!entry.anthropic_id
}

// Derive the variant row title from the catalog entry, used ONLY inside a
// grouped company block (company name is already in the group header there).
// Format: "<Plan> · <Region>" (region omitted when absent).
function variantRowTitle(entry: ProviderCatalogEntry): string {
  const plan = PLAN_LABELS[entry.plan]
  const region = entry.region ? REGION_LABELS[entry.region] : ''
  return region ? `${plan} · ${region}` : plan
}

// Backend seeds ~25 keyless template ModelConfigs (pkg/config/defaults.go)
// and GET /providers reports ALL of them forever as status:'disconnected'
// (Provider.yaml: "disconnected" = "no key available or fallback default
// entry"). Only providers the user actually attempted to configure (a stored
// key that connected, or one that errored) count as "configured" for the
// list/grouping/picker-exclusion purposes below — otherwise the empty state
// is unreachable and the picker wrongly excludes every never-touched
// template (e.g. OpenAI) on a fresh install.
function isConfigured(provider: Provider): boolean {
  return provider.status !== 'disconnected'
}

// Fallback display-name chain shared by the Sheet title and row titles when
// no catalog entry resolved for a provider (e.g. a manual/self-hosted id).
function displayName(provider: Provider | null | undefined, fallbackId: string): string {
  return provider?.display_name ?? provider?.name ?? fallbackId
}

// Resolve the id used for the outgoing PUT. Configure mode always submits
// the STORED provider id verbatim — canonicalizing an alias or anthropic_id
// sibling to the catalog's primary id here would silently fork the
// persisted config into two entries (the backend PUT matches by exact id and
// APPENDS on mismatch; there is no provider DELETE). Connect mode resolves
// the toggle's chosen endpoint id from the catalog entry.
function resolveSubmitId(target: SheetTarget, endpointFormat: EndpointFormat): string {
  if (target.mode === 'configure') return target.provider.id
  const { entry } = target
  return isDualWire(entry) && endpointFormat === 'anthropic' ? entry.anthropic_id : entry.id
}

// The canonical per-provider draft-state key (apiKeys/showKey/draftModels/
// newModel/saveValidation/endpointFormats) — the catalog entry id when one
// resolved, else the raw provider/target id. Stable across the connect-mode
// endpoint-format toggle and across which literal id (canonical, alias, or
// anthropic_id sibling) a configured provider happens to be stored under, so
// a typed-but-unsaved key or validation banner never gets stranded under a
// stale key (BUG: previously keyed by the raw id, which could diverge from
// the id the mutation's onSuccess cleanup used).
function sheetDraftKey(target: SheetTarget): string {
  if (target.mode === 'configure') return target.entry?.id ?? target.provider.id
  return target.entry.id
}

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

// Group configured providers by company using the migration resolver.
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
// exclude entries from the picker (FIX-3). Callers MUST pass an
// already-filtered (isConfigured) list — a raw GET /providers response
// includes ~25 forever-disconnected template rows that are never
// "configured" and must not exclude anything from the picker.
function configuredEntryIds(providers: Provider[]): Set<string> {
  const ids = new Set<string>()
  for (const p of providers) {
    const { entry } = resolveCatalogEntry(p.id)
    if (entry) ids.add(entry.id)
  }
  return ids
}

// Catalog entries grouped by company, excluding already-configured ids and
// filtered by a free-text search (company name, label, or alias). Returns
// ProviderPickerSheet's CatalogGroup shape — owned there (see its doc
// comment) since this function only builds the data to hand off to the
// picker's Sheet; nothing in this file reads a group's fields itself.
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
  entry: DualWireCatalogEntry
  value: EndpointFormat
  onChange: (v: EndpointFormat) => void
}) {
  const optionClass = (active: boolean) =>
    'flex-1 rounded-md border px-2.5 py-1.5 text-xs font-medium transition-colors ' +
    (active
      ? 'border-[var(--color-accent)] bg-[var(--color-accent)]/10 text-[var(--color-accent)]'
      : 'border-[var(--color-border)] text-[var(--color-secondary)] hover:border-[var(--color-muted)]')

  return (
    <div data-testid={`endpoint-format-toggle-${entry.id}`}>
      <label className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">
        Endpoint format
      </label>
      <div className="flex gap-1.5" role="group" aria-label="Endpoint format">
        <button tabIndex={0}
          type="button"
          aria-pressed={value === 'openai'}
          onClick={() => onChange('openai')}
          data-testid={`endpoint-format-openai-${entry.id}`}
          className={optionClass(value === 'openai')}
        >
          OpenAI-compatible (default)
        </button>
        <button tabIndex={0}
          type="button"
          aria-pressed={value === 'anthropic'}
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
  requestChange: (id: string, draftKey: string, key: string, models?: string[]) => void
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
  const entry = target.entry

  // For the provider object in configure mode.
  const provider = target.mode === 'configure' ? target.provider : null

  // Canonical draft-state key — see sheetDraftKey's doc comment. Stable
  // across the connect-mode toggle and across alias/anthropic_id storage.
  const draftKey = sheetDraftKey(target)

  const catalogMode = provider ? providerCatalogMode(provider) : 'live'
  const hint = PROVIDER_HINTS[providerId] ?? 'Enter your API key'

  const sheetTitle = entry ? entry.label : displayName(provider, providerId)
  const sheetDescription =
    target.mode === 'connect'
      ? 'Enter your API key to connect this provider.'
      : 'Update the API key for this provider.'

  // Endpoint-format toggle (FIX-5, connect mode only — see the read-only
  // display in the variant-info block for configure mode). Only meaningful
  // when the entry has a sibling anthropic_id. Default selection: 'openai'
  // (connect mode always starts from the primary endpoint).
  const endpointFormat = endpointFormats[draftKey] ?? 'openai'
  const resolvedSubmitId = resolveSubmitId(target, endpointFormat)

  // Configure mode: whether the STORED provider is reachable via the
  // anthropic_id sibling — threaded from resolveCatalogEntry at Sheet-open
  // time (single source of truth, see SheetTarget's doc comment).
  const viaAnthropicId = target.mode === 'configure' ? !!target.viaAnthropicId : false

  const handleClose = () => {
    onOpenChange(false)
    setSaveValidation((prev) => ({ ...prev, [draftKey]: undefined }))
    setDraftModels((prev) => {
      const { [draftKey]: _drop, ...rest } = prev
      return rest
    })
    setNewModel((prev) => ({ ...prev, [draftKey]: '' }))
  }

  const canSave = (() => {
    const keyChanged = !!(apiKeys[draftKey]?.trim())
    if (catalogMode !== 'manual') return keyChanged
    const modelsChanged = draftModels[draftKey] !== undefined
    return keyChanged || modelsChanged
  })()

  return (
    <Sheet open={open} onOpenChange={(o) => { if (!o) handleClose() }}>
      <SheetContent side="right" widthClass="w-[90vw] sm:max-w-lg" className="p-0" data-testid="provider-config-sheet">
        <SheetHeader className="px-6 pr-14">
          <div className="flex items-center gap-2 min-w-0">
            {entry && (
              <BrandIcon
                slug={entry.logoSlug}
                size={20}
                decorative
              />
            )}
            <SheetTitle>{sheetTitle}</SheetTitle>
          </div>
        </SheetHeader>
        <SheetDescription className="px-6 pt-3">{sheetDescription}</SheetDescription>

        <div className="px-6 space-y-5 overflow-y-auto pr-1 pt-4">
          {/* View-only variant info — Plan/Region/Endpoint(+format). Wire is a
              config detail, not a display row (FIX-5) — the Endpoint-format
              toggle below is interactive ONLY in connect mode; configure mode
              shows it here read-only (the backend PUT matches by exact id and
              APPENDS a new entry on mismatch — there is no provider DELETE,
              so the format can never be changed after the first connect). */}
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
                    {PLAN_LABELS[entry.plan]}
                  </span>
                </div>
                {entry.region && (
                  <div>
                    <span className="text-[var(--color-muted)] mr-1.5">Region</span>
                    <span
                      className="text-[var(--color-secondary)] font-medium"
                      data-testid="variant-region"
                    >
                      {REGION_LABELS[entry.region]}
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
                {target.mode === 'configure' && isDualWire(entry) && (
                  <div className="w-full">
                    <span className="text-[var(--color-muted)] mr-1.5">Endpoint format</span>
                    <span
                      className="text-[var(--color-secondary)] font-medium"
                      data-testid="variant-endpoint-format"
                    >
                      {viaAnthropicId ? 'Anthropic-compatible endpoint' : 'OpenAI-compatible endpoint'}
                    </span>
                  </div>
                )}
              </div>
              {target.mode === 'configure' && isDualWire(entry) && (
                <p className="text-xs text-[var(--color-muted)]" data-testid="endpoint-format-readonly-note">
                  Endpoint format is chosen when connecting a provider.
                </p>
              )}
            </div>
          )}

          {/* Endpoint-format toggle — connect mode only, dual-wire entries
              only (FIX-5). Configure mode shows the read-only field above. */}
          {target.mode === 'connect' && entry && isDualWire(entry) && (
            <EndpointFormatToggle
              entry={entry}
              value={endpointFormat}
              onChange={(v) => setEndpointFormats((prev) => ({ ...prev, [draftKey]: v }))}
            />
          )}

          {/* API Key input */}
          <div>
            <label className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">
              API Key
            </label>
            <div className="relative">
              <Input
                type={showKey[draftKey] ? 'text' : 'password'}
                value={apiKeys[draftKey] ?? ''}
                onChange={(e) =>
                  setApiKeys((prev) => ({ ...prev, [draftKey]: e.target.value }))
                }
                placeholder={hint}
                className="pr-9 font-mono text-xs"
                autoComplete="off"
                data-testid={`api-key-input-${providerId}`}
              />
              <button tabIndex={0}
                type="button"
                onClick={() =>
                  setShowKey((prev) => ({ ...prev, [draftKey]: !prev[draftKey] }))
                }
                className="absolute right-2.5 top-1/2 -translate-y-1/2 text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
                aria-label={showKey[draftKey] ? 'Hide API key' : 'Show API key'}
              >
                {showKey[draftKey] ? <EyeSlash size={14} /> : <Eye size={14} />}
              </button>
            </div>
          </div>

          {/* Manual model-slug editor — only for configure mode on manual providers */}
          {provider && catalogMode === 'manual' && (() => {
            const models = draftModels[draftKey] ?? provider.models ?? []
            const pendingSlug = (newModel[draftKey] ?? '').trim()
            const addSlug = () => {
              if (!pendingSlug || models.includes(pendingSlug)) return
              setDraftModels((prev) => ({
                ...prev,
                [draftKey]: [...models, pendingSlug],
              }))
              setNewModel((prev) => ({ ...prev, [draftKey]: '' }))
            }
            const removeSlug = (slug: string) => {
              setDraftModels((prev) => ({
                ...prev,
                [draftKey]: models.filter((m) => m !== slug),
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
                          <button tabIndex={0}
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
                    value={newModel[draftKey] ?? ''}
                    onChange={(e) =>
                      setNewModel((prev) => ({ ...prev, [draftKey]: e.target.value }))
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
          {saveValidation[draftKey] && (
            <ProviderValidationBanner
              validation={saveValidation[draftKey]}
              data-testid={`save-validation-banner-${providerId}`}
            />
          )}

          {/* Footer actions */}
          <div className="flex justify-between gap-2 pt-2">
            <div className="flex gap-2">
              {provider && catalogMode === 'live' && (
                <button tabIndex={0}
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
                <button tabIndex={0}
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
                  const key = (apiKeys[draftKey] ?? '').trim()
                  const models =
                    catalogMode === 'manual' && provider
                      ? (draftModels[draftKey] ?? provider.models ?? [])
                      : undefined
                  requestChange(resolvedSubmitId, draftKey, key, models)
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
    mutationFn: ({ id, key, token, models }: { id: string; draftKey: string; key: string; token: string; models?: string[] }) =>
      configureProvider(id, key === '' ? undefined : key, undefined, undefined, token, models),
    // Destructure `draftKey` (NOT `id`) — the canonical draft-state key set by
    // requestChange, so the typed key / validation banner is cleared under the
    // SAME key the Sheet is reading from, regardless of which literal id the
    // PUT actually submitted (BUG #2: previously cleared by mutation `id`,
    // which can diverge from the draft key on alias / anthropic_id storage).
    onSuccess: (provider, { draftKey }) => {
      queryClient.invalidateQueries({ queryKey: ['providers'] })
      const validation = provider.validation
      if (validation?.outcome === 'invalid_key') {
        // invalid_key is the ONE outcome the contract says blocks a usable
        // provider. Do NOT report a green success — surface an error and keep
        // the sheet open with the banner so the user can correct the key.
        setSaveValidation((prev) => ({ ...prev, [draftKey]: validation }))
        addToast({
          message: validation.message ?? 'Key rejected — the provider was saved but will not work until you fix the key',
          variant: 'error',
        })
      } else if (validation && validation.outcome !== 'valid') {
        // Non-blocking outcomes (no_credit / unreachable / restricted): saved
        // successfully; keep the sheet open so the amber warning banner is
        // visible. (Unchanged from prior behavior.)
        setSaveValidation((prev) => ({ ...prev, [draftKey]: validation }))
        addToast({ message: 'Provider saved', variant: 'success' })
      } else {
        setSaveValidation((prev) => ({ ...prev, [draftKey]: undefined }))
        addToast({ message: 'Provider saved', variant: 'success' })
        setSheetOpen(false)
      }
      setPending(null)
      setApiKeys((prev) => ({ ...prev, [draftKey]: '' }))
    },
    onError: (err: Error) => {
      addToast({ message: getErrorMessage(err, 'Provider save failed'), variant: 'error' })
      setPending(null)
    },
  })

  const requestChange = (id: string, draftKey: string, key: string, models?: string[]) => {
    setSaveValidation((prev) => ({ ...prev, [draftKey]: undefined }))
    setPending({ id, draftKey, key, models })
    setReauthOpen(true)
  }

  const onReAuthConfirmed = (token: string) => {
    if (!pending) return
    applyChange({ id: pending.id, draftKey: pending.draftKey, key: pending.key, token, models: pending.models })
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
      addToast({ message: getErrorMessage(err, 'Model refresh failed'), variant: 'error' })
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
      addToast({ message: getErrorMessage(err, 'Connection test failed'), variant: 'error' })
    } finally {
      setTesting((prev) => ({ ...prev, [id]: false }))
    }
  }

  const openConfigureSheet = (provider: Provider) => {
    const { entry, viaAnthropicId } = resolveCatalogEntry(provider.id)
    setSheetTarget({ mode: 'configure', provider, entry, viaAnthropicId })
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

  // Only providers the user actually attempted to configure — see
  // isConfigured's doc comment (backend seeds ~25 forever-disconnected
  // template rows that must never appear as "configured").
  const configuredProviders = useMemo(() => providers.filter(isConfigured), [providers])
  const groups = groupProviders(configuredProviders)
  const hasConfigured = configuredProviders.length > 0

  const excludeIds = useMemo(() => configuredEntryIds(configuredProviders), [configuredProviders])
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
            after the first), and also on a fetch error so the CTA stays
            reachable even though the body below suppresses the empty-state's
            own copy of this button to avoid showing two conflicting messages
            at once. */}
        {(hasConfigured || providersError) && (
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

      {providersError ? (
        <p className="text-sm text-red-400" data-testid="providers-error">
          Failed to load providers. Please try again.
        </p>
      ) : isLoading ? (
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
              const title = entry ? entry.label : displayName(provider, provider.id)
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
                    are configured (FIX-2). The per-group "Add another…"
                    control is gone (FIX-1). */}
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
                      title={entry ? variantRowTitle(entry) : displayName(provider, provider.id)}
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
            // Clean up on close — same canonical draft key the Sheet reads
            // from (sheetDraftKey), so the banner never survives under a
            // stale key (BUG #2, see PendingProviderChange's doc comment).
            if (sheetTarget) {
              setSaveValidation((prev) => ({ ...prev, [sheetDraftKey(sheetTarget)]: undefined }))
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
