// ProvidersSection.tsx — ADR-031 Track 1: Providers redesign.
//
// Implements:
//   FR-008  configured-only list; empty → catalog roster
//   FR-009  Sheet slide-out for config AND connect (no inline expand)
//   FR-010  company-grouped, binding-first variant rows
//   FR-011  view-only Plan/Region/Wire/Endpoint in Sheet; only API key editable
//   FR-012  migration: alias→canonical, self-hosted→group, unknown→generic
//   FR-013  <BrandIcon> + lettermark fallback
//   FR-014  <BrandDisclaimer> wherever marks appear
//   US-7    label/subtitle/logoSlug verbatim from PROVIDER_CATALOG

import { useState } from 'react'
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

// Map a plan value to a human-readable access type label (FR-006).
function planLabel(plan: 'standard-api' | 'coding-plan'): string {
  if (plan === 'coding-plan') return 'Coding Plan'
  return 'Standard API'
}

// Map a region value to a human-readable string.
function regionLabel(region?: 'intl' | 'china' | 'us'): string {
  if (region === 'intl') return 'International'
  if (region === 'china') return 'China'
  if (region === 'us') return 'US'
  return ''
}

// Derive the variant row title from the catalog entry.
// Format: "<Access Type> · <Region>" (region omitted when absent).
// This is the binding-first title without the redundant company prefix (FR-010).
function variantRowTitle(entry: ProviderCatalogEntry): string {
  const plan = planLabel(entry.plan)
  const region = regionLabel(entry.region)
  return region ? `${plan} · ${region}` : plan
}

// Wire badge text (FR-005).
function wireBadgeLabel(wire: 'openai-compatible' | 'anthropic'): string {
  return wire === 'anthropic' ? 'Anthropic-compatible' : 'OpenAI-compatible'
}

// Group configured providers by company using the migration resolver.
interface ProviderGroup {
  group: string
  logoSlug?: string
  items: Array<{ provider: Provider; entry?: ProviderCatalogEntry }>
}

function groupProviders(providers: Provider[]): ProviderGroup[] {
  const order: string[] = []
  const map = new Map<string, ProviderGroup>()

  for (const provider of providers) {
    const { entry, group } = resolveCatalogEntry(provider.id)
    if (!map.has(group)) {
      order.push(group)
      map.set(group, {
        group,
        logoSlug: entry?.logoSlug,
        items: [],
      })
    }
    map.get(group)!.items.push({ provider, entry })
  }

  return order.map((g) => map.get(g)!)
}

// Empty-state roster: catalog entries grouped by company.
interface RosterGroup {
  company: string
  logoSlug: string
  entries: ProviderCatalogEntry[]
}

function buildRoster(): RosterGroup[] {
  // The roster IS the catalog — every user-facing provider is connectable,
  // including `ollama` (a first-class "Ollama (local)" catalog entry that
  // onboarding also offers; excluding it here would make Settings inconsistent
  // with onboarding). Genuinely not-in-catalog runtimes (litellm/vllm) simply
  // aren't in PROVIDER_CATALOG, so they never appear here anyway.
  const order: string[] = []
  const map = new Map<string, RosterGroup>()

  for (const entry of PROVIDER_CATALOG) {
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

const PROVIDER_ROSTER = buildRoster()

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
          {/* View-only variant info (FR-011) */}
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
                <div>
                  <span className="text-[var(--color-muted)] mr-1.5">Wire</span>
                  <Badge
                    variant="muted"
                    className="font-normal text-xs"
                    data-testid="variant-wire-badge"
                  >
                    {wireBadgeLabel(entry.wire)}
                  </Badge>
                </div>
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
                  requestChange(providerId, key, models)
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

  const [apiKeys, setApiKeys] = useState<Record<string, string>>({})
  const [showKey, setShowKey] = useState<Record<string, boolean>>({})
  const [testing, setTesting] = useState<Record<string, boolean>>({})
  const [refreshing, setRefreshing] = useState<Record<string, boolean>>({})
  const [saveValidation, setSaveValidation] = useState<Record<string, ProviderValidation | undefined>>({})
  const [testValidation, setTestValidation] = useState<Record<string, ProviderValidation | undefined>>({})
  const [draftModels, setDraftModels] = useState<Record<string, string[]>>({})
  const [newModel, setNewModel] = useState<Record<string, string>>({})

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

  const groups = groupProviders(providers)
  const hasConfigured = providers.length > 0

  return (
    <div className="space-y-4">
      <div>
        <h2 className="font-headline font-bold text-base text-[var(--color-secondary)]">Providers</h2>
        <p className="text-xs text-[var(--color-muted)] mt-0.5">
          API keys are stored encrypted in credentials.json — never in config.json.
        </p>
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
        /* ── Configured-only list, grouped by company ── */
        <div className="space-y-4">
          {groups.map((group) => (
            <div key={group.group} data-testid={`provider-group-${group.group}`}>
              {/* Company group header */}
              <div className="flex items-center justify-between mb-1.5">
                <div className="flex items-center gap-2">
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
                {/* "Add another…" for catalog-resolved groups */}
                {group.items[0]?.entry && (
                  <button
                    type="button"
                    className="text-xs text-[var(--color-accent)] hover:underline"
                    onClick={() => {
                      const entry = group.items[0]?.entry
                      if (entry) openConnectSheet(entry)
                    }}
                    data-testid={`add-another-${group.group}`}
                  >
                    + Add another…
                  </button>
                )}
              </div>

              <div className="space-y-1.5">
                {group.items.map(({ provider, entry }) => {
                  const connected = provider.status === 'connected'
                  const catalogMode = providerCatalogMode(provider)
                  const rowTitle = entry ? variantRowTitle(entry) : (provider.display_name ?? provider.name ?? provider.id)
                  const subtitle = entry?.subtitle

                  return (
                    <div
                      key={provider.id}
                      className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
                      data-testid={`provider-row-${provider.id}`}
                    >
                      {/* Test-validation warning banner */}
                      {testValidation[provider.id] && (
                        <ProviderValidationBanner
                          validation={testValidation[provider.id]}
                          data-testid={`test-validation-banner-${provider.id}`}
                        />
                      )}

                      <div className="flex items-center gap-3 px-4 py-3">
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2 flex-wrap">
                            <span
                              className="text-sm font-medium text-[var(--color-secondary)]"
                              data-testid={`provider-row-title-${provider.id}`}
                            >
                              {rowTitle}
                            </span>
                            {/* Wire badge (FR-010 / FR-005) */}
                            {entry && (
                              <Badge
                                variant="muted"
                                className="font-normal text-xs"
                                data-testid={`wire-badge-${provider.id}`}
                              >
                                {wireBadgeLabel(entry.wire)}
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
                            onClick={() => openConfigureSheet(provider)}
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
                })}
              </div>
            </div>
          ))}

          {/* Trademark disclaimer whenever brand marks are shown */}
          <BrandDisclaimer className="mt-2" />
        </div>
      ) : (
        /* ── Empty-state roster (FR-008, US-3) ── */
        <div className="space-y-4" data-testid="provider-roster">
          <p className="text-sm text-[var(--color-muted)]">
            No providers configured yet. Connect one to get started.
          </p>

          {PROVIDER_ROSTER.map((rosterGroup) => (
            <div key={rosterGroup.company} data-testid={`roster-group-${rosterGroup.company}`}>
              <div className="flex items-center gap-2 mb-1.5">
                <BrandIcon slug={rosterGroup.logoSlug} size={18} decorative />
                <span className="text-xs font-semibold text-[var(--color-secondary)] uppercase tracking-wide">
                  {rosterGroup.company}
                </span>
              </div>

              <div className="space-y-1">
                {rosterGroup.entries.map((entry) => (
                  <div
                    key={entry.id}
                    className="flex items-center justify-between rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-2.5"
                    data-testid={`roster-entry-${entry.id}`}
                  >
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium text-[var(--color-secondary)]">
                          {variantRowTitle(entry)}
                        </span>
                        <Badge variant="muted" className="text-[11px]" data-testid={`roster-wire-${entry.id}`}>
                          {wireBadgeLabel(entry.wire)}
                        </Badge>
                      </div>
                      <p className="text-xs text-[var(--color-muted)] mt-0.5">{entry.subtitle}</p>
                    </div>
                    <Button
                      size="sm"
                      variant="outline"
                      className="h-7 px-3 text-xs shrink-0"
                      onClick={() => openConnectSheet(entry)}
                      data-testid={`connect-btn-${entry.id}`}
                    >
                      Connect
                    </Button>
                  </div>
                ))}
              </div>
            </div>
          ))}

          {/* Trademark disclaimer for the roster (brand marks displayed) */}
          <BrandDisclaimer className="mt-2" />
        </div>
      )}

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
