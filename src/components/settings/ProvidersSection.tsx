// ProvidersSection.tsx — Provider UX fixes (supersedes the ADR-031 Track 1
// roster/grouping/terminology choices per docs/internal/specs/provider-ux-fixes-plan.md).
//
// Corrected domain model: a provider is a global, single-instance API config.
// There is exactly ONE config per catalog entry — no "instances", no
// workspace/agent binding, no "Add another". The only real variant axes are
// plan (pay-as-you-go vs Coding Plan) and region (intl/china/us).
//
// Catalog source (ADR-068 FR-037 / T068-05): the registry-fed document from
// GET /api/v1/providers/catalog (src/lib/api.ts::fetchProvidersCatalog), typed
// as the generated CatalogProvider. There is NO bundled catalog (SC-010).
// GET /providers returns configured providers only (FR-011a) — no template
// rows to filter out. The retired refresh-models action is replaced by
// *Check with my account* (FR-031), wired in T068-27.
//
// Implements:
//   FIX-1  no per-company "Add another…" control
//   FIX-2  company group header only when ≥2 configured variants; else a flat row
//   FIX-3  configured-only list; empty state + always-visible "Connect a provider"
//          opens an on-demand picker Sheet (search + catalog grouped by company,
//          excluding already-configured entries)
//   FIX-4  real terminology — "Pay-as-you-go API" / "Coding Plan", no "Standard API"
//   FIX-6  migration: alias → canonical, self-hosted→group, unknown→generic
//   FR-009 Sheet slide-out for config AND connect (no inline expand)
//   FR-013 <BrandIcon> + lettermark fallback
//   FR-014 <BrandDisclaimer> wherever marks appear

import { useState, useMemo, useEffect } from 'react'
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
  testProvider,
  signOutProvider,
  getErrorMessage,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { PROVIDER_HINTS } from '@/lib/constants'
import { planLabel, regionLabel } from '@/lib/providerLabels'
import { catalogEndpointHint, catalogLabel, catalogLogoSlug, catalogVariantTitle } from '@/lib/catalogDisplay'
import { providerCatalogMode } from '@/lib/agents/providerCatalog'
import { providersCatalogQueryOptions } from '@/lib/providersCatalogQuery'
import { DRAFT_DISCARD_PROMPT, draftCloseDecision, type DraftCloseAction } from '@/hooks/use-draft-guard'
import { ReAuthDialog } from './ReAuthDialog'
import { ProviderValidationBanner } from '@/components/providers/ProviderValidationBanner'
import { resolveCatalogEntry } from '@/lib/providerMigration'
import { ProviderRow } from './ProviderRow'
import { SignInDialog } from '@/components/providers/SignInDialog'
import { ProviderPickerSheet } from './ProviderPickerSheet'
import type { CatalogGroup } from './ProviderPickerSheet'
import type { ProviderValidation, Provider } from '@/lib/api/generated/openapi-types'
import type { CatalogProvider } from '@/lib/api/generated/openapi-types'

// A pending provider edit captured before the re-auth prompt; replayed once the
// consent token is minted. `id` is the id submitted to the PUT (resolveSubmitId).
// `draftKey` is the canonical per-provider draft-state key (sheetDraftKey) —
// carried through so the mutation's success/close handlers clear the SAME key
// the Sheet read from, even when `id` diverges from it (alias storage). `key` carries an
// API-key change (empty string = no key change); `models` carries a manual
// model-slug catalogue replacement (undefined = leave the catalogue unchanged).
type PendingProviderChange = {
  id: string
  draftKey: string
  key: string
  models?: string[]
}

// The item shown in the Sheet — either an existing configured provider (configure
// mode) or a catalog entry for first-time setup (connect mode).
type SheetTarget =
  | { mode: 'configure'; provider: Provider; entry?: CatalogProvider }
  | { mode: 'connect'; entry: CatalogProvider }

// Fallback display-name chain shared by the Sheet title and row titles when
// no catalog entry resolved for a provider (e.g. a manual/self-hosted id).
function displayName(provider: Provider | null | undefined, fallbackId: string): string {
  return provider?.display_name ?? provider?.name ?? fallbackId
}

// Resolve the id used for the outgoing PUT. Configure mode always submits
// the STORED provider id verbatim — canonicalizing an alias to the catalog's
// primary id here would silently fork the persisted config into two entries
// (the backend PUT matches by exact id and APPENDS on mismatch). Connect mode
// submits the catalog entry's canonical id.
function resolveSubmitId(target: SheetTarget): string {
  return target.mode === 'configure' ? target.provider.id : target.entry.id
}

// The canonical per-provider draft-state key (apiKeys/showKey/draftModels/
// newModel/saveValidation) — the catalog entry id when one resolved, else the
// raw provider/target id. Stable across which literal id (canonical or alias)
// a configured provider happens to be stored under, so a typed-but-unsaved
// key or validation banner never gets stranded under a stale key (BUG:
// previously keyed by the raw id, which could diverge from the id the
// mutation's onSuccess cleanup used).
function sheetDraftKey(target: SheetTarget): string {
  if (target.mode === 'configure') return target.entry?.id ?? target.provider.id
  return target.entry.id
}

interface ProviderGroupItem {
  provider: Provider
  entry?: CatalogProvider
}

interface ProviderGroup {
  group: string
  logoSlug?: string
  items: ProviderGroupItem[]
}

// Group configured providers by company using the migration resolver over
// the fetched catalog.
function groupProviders(catalog: CatalogProvider[], providers: Provider[]): ProviderGroup[] {
  const order: string[] = []
  const map = new Map<string, ProviderGroup>()

  for (const provider of providers) {
    const { entry, group } = resolveCatalogEntry(catalog, provider.id)
    if (!map.has(group)) {
      order.push(group)
      map.set(group, {
        group,
        logoSlug: entry ? catalogLogoSlug(entry) : undefined,
        items: [],
      })
    }
    map.get(group)!.items.push({ provider, entry })
  }

  return order.map((g) => map.get(g)!)
}

// The set of catalog entry ids that are already configured — an id is
// "configured" if ANY fetched provider.id resolves to that entry (via
// resolveCatalogEntry, including alias matches). Used to exclude entries
// from the picker (FIX-3). GET /providers returns configured rows only
// (ADR-068 FR-011a), so the whole response participates.
function configuredEntryIds(catalog: CatalogProvider[], providers: Provider[]): Set<string> {
  const ids = new Set<string>()
  for (const p of providers) {
    const { entry } = resolveCatalogEntry(catalog, p.id)
    if (entry) ids.add(entry.id)
  }
  return ids
}

// Catalog entries grouped by company, excluding already-configured ids and
// filtered by a free-text search (company name, label, or alias). Returns
// ProviderPickerSheet's CatalogGroup shape — owned there (see its doc
// comment) since this function only builds the data to hand off to the
// picker's Sheet; nothing in this file reads a group's fields itself.
function buildCatalogGroups(catalog: CatalogProvider[], excludeIds: Set<string>, query: string): CatalogGroup[] {
  const q = query.trim().toLowerCase()
  const order: string[] = []
  const map = new Map<string, CatalogGroup>()

  for (const entry of catalog) {
    if (excludeIds.has(entry.id)) continue
    if (q) {
      const haystack = [entry.company, entry.name, catalogLabel(entry), ...entry.aliases]
        .join(' ')
        .toLowerCase()
      if (!haystack.includes(q)) continue
    }
    if (!map.has(entry.company)) {
      order.push(entry.company)
      map.set(entry.company, {
        company: entry.company,
        logoSlug: catalogLogoSlug(entry),
        entries: [],
      })
    }
    map.get(entry.company)!.entries.push(entry)
  }

  return order.map((c) => map.get(c)!)
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
  /**
   * Per-draft "what is in the field has already been saved" flag (FR-033,
   * "saved = clean"). Owned by the parent because the successful-save handler
   * lives there; reset the moment the operator types again.
   */
  keySaved: Record<string, boolean>
  setKeySaved: React.Dispatch<React.SetStateAction<Record<string, boolean>>>
  draftModels: Record<string, string[]>
  setDraftModels: React.Dispatch<React.SetStateAction<Record<string, string[]>>>
  newModel: Record<string, string>
  setNewModel: React.Dispatch<React.SetStateAction<Record<string, string>>>
  saveValidation: Record<string, ProviderValidation | undefined>
  setSaveValidation: React.Dispatch<React.SetStateAction<Record<string, ProviderValidation | undefined>>>
  isSaving: boolean
  requestChange: (id: string, draftKey: string, key: string, models?: string[]) => void
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
  keySaved,
  setKeySaved,
  draftModels,
  setDraftModels,
  newModel,
  setNewModel,
  saveValidation,
  setSaveValidation,
  isSaving,
  requestChange,
  testing,
  handleTest,
}: ProviderConfigSheetProps) {
  // FR-033: an accidental close (Esc / overlay) with a dirty key does not
  // close — it asks. This flag is the inline "Discard key?" prompt's state.
  // Declared before the `!target` guard so the hook order never changes.
  const [discardPrompt, setDiscardPrompt] = useState(false)

  // A freshly opened sheet never inherits a stale prompt.
  useEffect(() => {
    if (!open) setDiscardPrompt(false)
  }, [open])

  if (!target) return null

  // Determine the provider id and entry in both modes.
  const providerId = target.mode === 'configure' ? target.provider.id : target.entry.id
  const entry = target.entry

  // For the provider object in configure mode.
  const provider = target.mode === 'configure' ? target.provider : null

  // Canonical draft-state key — see sheetDraftKey's doc comment. Stable
  // across alias storage.
  const draftKey = sheetDraftKey(target)

  const catalogMode = provider ? providerCatalogMode(provider) : 'live'
  const hint = PROVIDER_HINTS[providerId] ?? 'Enter your API key'

  const sheetTitle = entry ? catalogLabel(entry) : displayName(provider, providerId)
  const sheetDescription =
    target.mode === 'connect'
      ? 'Enter your API key to connect this provider.'
      : 'Update the API key for this provider.'

  const resolvedSubmitId = resolveSubmitId(target)

  // The one place the sheet actually goes away. Clears every per-draft scrap,
  // the typed key included — FR-033 only ever reaches here on a decision that
  // says the draft may be dropped.
  const closeAndClear = () => {
    setDiscardPrompt(false)
    onOpenChange(false)
    setApiKeys((prev) => ({ ...prev, [draftKey]: '' }))
    setKeySaved((prev) => ({ ...prev, [draftKey]: false }))
    setSaveValidation((prev) => ({ ...prev, [draftKey]: undefined }))
    setDraftModels((prev) => {
      const rest = { ...prev }
      delete rest[draftKey]
      return rest
    })
    setNewModel((prev) => ({ ...prev, [draftKey]: '' }))
  }

  /**
   * FR-033's close matrix, applied. `cancel` is the operator saying so out
   * loud (the Cancel button); `esc` / `overlay` are the accidents that used to
   * destroy a pasted key silently. The rule itself lives in
   * `@/hooks/use-draft-guard` so the five spec rows are testable on their own.
   *
   * The Sheet's own X button routes through `esc`: it is a close gesture, not
   * the explicit discard that Cancel is, so it gets the same protection.
   */
  const handleClose = (action: DraftCloseAction) => {
    const decision = draftCloseDecision({
      value: apiKeys[draftKey],
      saved: !!keySaved[draftKey],
      action,
    })
    if (decision.prompt) {
      setDiscardPrompt(true)
      return
    }
    closeAndClear()
  }

  const canSave = (() => {
    const keyChanged = !!(apiKeys[draftKey]?.trim())
    if (catalogMode !== 'manual') return keyChanged
    const modelsChanged = draftModels[draftKey] !== undefined
    return keyChanged || modelsChanged
  })()

  return (
    <Sheet open={open} onOpenChange={(o) => { if (!o) handleClose('esc') }}>
      <SheetContent
        side="right"
        widthClass="w-[90vw] sm:max-w-lg"
        className="p-0"
        data-testid="provider-config-sheet"
        // FR-033: preventDefault keeps Radix from unmounting the sheet, so the
        // prompt renders inside a sheet that never lost focus (WCAG 3.2.1).
        onEscapeKeyDown={(e) => {
          e.preventDefault()
          if (discardPrompt) {
            setDiscardPrompt(false)
            return
          }
          handleClose('esc')
        }}
        // Deliberately onPointerDownOutside, NOT onInteractOutside: the latter
        // also fires on focus-outside, which is exactly what happens when the
        // re-auth dialog opens on top of the sheet — the operator never touched
        // the overlay, so it must not be read as a dismissal.
        onPointerDownOutside={(e) => {
          e.preventDefault()
          if (discardPrompt) return
          handleClose('overlay')
        }}
      >
        <SheetHeader className="px-6 pr-14">
          <div className="flex items-center gap-2 min-w-0">
            {entry && (
              <BrandIcon
                slug={catalogLogoSlug(entry)}
                size={20}
                decorative
              />
            )}
            <SheetTitle>{sheetTitle}</SheetTitle>
          </div>
        </SheetHeader>
        <SheetDescription className="px-6 pt-3">{sheetDescription}</SheetDescription>

        <div className="px-6 space-y-5 overflow-y-auto pr-1 pt-4">
          {/* View-only variant info — Plan/Region/Endpoint, derived from the
              fetched CatalogProvider (src/lib/catalogDisplay.ts). */}
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
                    {catalogEndpointHint(entry)}
                  </span>
                </div>
              </div>
            </div>
          )}

          {/* API Key input */}
          <div>
            <label htmlFor={`api-key-input-${draftKey}`} className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">
              API Key
            </label>
            <div className="relative">
              <Input
                id={`api-key-input-${draftKey}`}
                type={showKey[draftKey] ? 'text' : 'password'}
                value={apiKeys[draftKey] ?? ''}
                onChange={(e) => {
                  setApiKeys((prev) => ({ ...prev, [draftKey]: e.target.value }))
                  // Typing makes the field dirty again (FR-033).
                  setKeySaved((prev) => ({ ...prev, [draftKey]: false }))
                }}
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
                <label htmlFor={`add-model-input-${draftKey}`} className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">
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
                    id={`add-model-input-${draftKey}`}
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

          {/* FR-033 inline discard prompt — rendered inside the sheet so the
              question never steals focus from it (WCAG 3.2.1). */}
          {discardPrompt && (
            <div
              role="alertdialog"
              aria-labelledby={`discard-key-title-${draftKey}`}
              aria-describedby={`discard-key-body-${draftKey}`}
              data-testid="discard-key-prompt"
              className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-4 py-3 space-y-2"
            >
              <p
                id={`discard-key-title-${draftKey}`}
                className="text-sm font-medium text-[var(--color-secondary)]"
              >
                {DRAFT_DISCARD_PROMPT.title}
              </p>
              <p id={`discard-key-body-${draftKey}`} className="text-xs text-[var(--color-muted)]">
                The key you typed has not been saved yet.
              </p>
              <div className="flex justify-end gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  autoFocus
                  onClick={() => setDiscardPrompt(false)}
                  data-testid="discard-key-keep"
                >
                  {DRAFT_DISCARD_PROMPT.cancel}
                </Button>
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={closeAndClear}
                  data-testid="discard-key-discard"
                >
                  {DRAFT_DISCARD_PROMPT.confirm}
                </Button>
              </div>
            </div>
          )}

          {/* Footer actions */}
          <div className="flex justify-between gap-2 pt-2">
            <div className="flex gap-2">
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
                onClick={() => handleClose('cancel')}
                data-testid={`cancel-provider-${providerId}`}
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
  // FR-033 "saved = clean": true once the draft under this key has been saved,
  // cleared the moment the operator types again. Never holds the key itself.
  const [keySaved, setKeySaved] = useState<Record<string, boolean>>({})
  const [testing, setTesting] = useState<Record<string, boolean>>({})
  const [saveValidation, setSaveValidation] = useState<Record<string, ProviderValidation | undefined>>({})
  const [testValidation, setTestValidation] = useState<Record<string, ProviderValidation | undefined>>({})
  const [draftModels, setDraftModels] = useState<Record<string, string[]>>({})
  const [newModel, setNewModel] = useState<Record<string, string>>({})

  const [pending, setPending] = useState<PendingProviderChange | null>(null)
  const [reauthOpen, setReauthOpen] = useState(false)

  // Sign-in dialog state (ADR-068 §8b, T068-33) — which provider row's
  // SignInDialog is open, if any.
  const [signInTarget, setSignInTarget] = useState<{ id: string; label: string } | null>(null)
  const [signInOpen, setSignInOpen] = useState(false)

  const { data: providers = [], isLoading, isError: providersError } = useQuery({
    queryKey: ['providers'],
    queryFn: fetchProviders,
  })

  // Registry-fed catalog (ADR-068 FR-037). The ETag re-validation cadence
  // (Settings open + every 15 min) is the shared policy in
  // providersCatalogQuery.ts — never re-specified at a call site.
  const { data: catalogDoc } = useQuery(providersCatalogQueryOptions())
  const catalog = useMemo(() => catalogDoc?.providers ?? [], [catalogDoc])

  const { mutate: applyChange, isPending: isSaving } = useMutation({
    mutationFn: ({ id, key, token, models }: { id: string; draftKey: string; key: string; token: string; models?: string[] }) =>
      configureProvider(id, key === '' ? undefined : key, undefined, undefined, token, models),
    // Destructure `draftKey` (NOT `id`) — the canonical draft-state key set by
    // requestChange, so the typed key / validation banner is cleared under the
    // SAME key the Sheet is reading from, regardless of which literal id the
    // PUT actually submitted (BUG #2: previously cleared by mutation `id`,
    // which can diverge from the draft key on alias storage).
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
      // The draft is now on the server: closing the sheet loses nothing
      // (FR-033, "saved = clean").
      setKeySaved((prev) => ({ ...prev, [draftKey]: true }))
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
    const { entry } = resolveCatalogEntry(catalog, provider.id)
    setSheetTarget({ mode: 'configure', provider, entry })
    setSheetOpen(true)
  }

  const openConnectSheet = (entry: CatalogProvider) => {
    setSheetTarget({ mode: 'connect', entry })
    setSheetOpen(true)
  }

  const openPicker = () => {
    setPickerQuery('')
    setPickerOpen(true)
  }

  const openSignInDialog = (id: string, label: string) => {
    setSignInTarget({ id, label })
    setSignInOpen(true)
  }

  const { mutate: doSignOut, isPending: isSigningOut } = useMutation({
    mutationFn: (id: string) => signOutProvider(id),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ['providers'] })
      if (result.success) {
        addToast({ message: 'Signed out', variant: 'success' })
      } else {
        addToast({ message: result.error ?? 'Sign out failed', variant: 'error' })
      }
    },
    onError: (err: Error) => {
      addToast({ message: getErrorMessage(err, 'Sign out failed'), variant: 'error' })
    },
  })

  // GET /providers returns configured providers only (ADR-068 FR-011a).
  const groups = groupProviders(catalog, providers)
  const hasConfigured = providers.length > 0

  const excludeIds = useMemo(() => configuredEntryIds(catalog, providers), [catalog, providers])
  const catalogGroups = useMemo(
    () => buildCatalogGroups(catalog, excludeIds, pickerQuery),
    [catalog, excludeIds, pickerQuery],
  )
  const allConfigured = catalog.length > 0 && excludeIds.size >= catalog.length

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
              const { provider, entry } = group.items[0]
              const title = entry ? catalogLabel(entry) : displayName(provider, provider.id)
              return (
                <ProviderRow
                  key={provider.id}
                  provider={provider}
                  entry={entry}
                  title={title}
                  showIcon
                  onConfigure={() => openConfigureSheet(provider)}
                  onSignIn={() => openSignInDialog(provider.id, title)}
                  onSignOut={() => doSignOut(provider.id)}
                  signingIn={signInOpen && signInTarget?.id === provider.id}
                  signingOut={isSigningOut}
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
                  {group.items.map(({ provider, entry }) => (
                    <ProviderRow
                      key={provider.id}
                      provider={provider}
                      entry={entry}
                      title={entry ? catalogVariantTitle(entry) : displayName(provider, provider.id)}
                      showIcon={false}
                      onConfigure={() => openConfigureSheet(provider)}
                      onSignIn={() => openSignInDialog(provider.id, entry ? catalogVariantTitle(entry) : displayName(provider, provider.id))}
                      onSignOut={() => doSignOut(provider.id)}
                      signingIn={signInOpen && signInTarget?.id === provider.id}
                      signingOut={isSigningOut}
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
          // ADR-068 FR-005: a catalog row offering sign_in (openai-chatgpt /
          // codex-cli / xai once configured) opens the SignInDialog directly
          // instead of the API-key connect Sheet — there is no key to type.
          if (entry.auth_methods.includes('sign_in')) {
            openSignInDialog(entry.id, catalogLabel(entry))
          } else {
            openConnectSheet(entry)
          }
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
        keySaved={keySaved}
        setKeySaved={setKeySaved}
        draftModels={draftModels}
        setDraftModels={setDraftModels}
        newModel={newModel}
        setNewModel={setNewModel}
        saveValidation={saveValidation}
        setSaveValidation={setSaveValidation}
        isSaving={isSaving}
        requestChange={requestChange}
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

      {signInTarget && (
        <SignInDialog
          open={signInOpen}
          onOpenChange={(o) => {
            setSignInOpen(o)
            if (!o) setSignInTarget(null)
          }}
          providerId={signInTarget.id}
          providerLabel={signInTarget.label}
          onSignedIn={() => {
            queryClient.invalidateQueries({ queryKey: ['providers'] })
          }}
        />
      )}
    </div>
  )
}
