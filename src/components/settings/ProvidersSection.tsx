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
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { fetchProviders, configureProvider, refreshProviderModels, testProvider, isApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { PROVIDER_HINTS } from '@/lib/constants'
import { providerCatalogMode } from '@/lib/agents/providerCatalog'
import { ReAuthDialog } from './ReAuthDialog'

// A pending provider edit captured before the re-auth prompt; replayed once the
// consent token is minted. `key` carries an API-key change (empty string = no
// key change); `models` carries a manual model-slug catalogue replacement
// (undefined = leave the catalogue unchanged).
type PendingProviderChange = {
  id: string
  key: string
  models?: string[]
}

export function ProvidersSection() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const [expandedProvider, setExpandedProvider] = useState<string | null>(null)
  const [apiKeys, setApiKeys] = useState<Record<string, string>>({})
  const [showKey, setShowKey] = useState<Record<string, boolean>>({})
  const [testing, setTesting] = useState<Record<string, boolean>>({})
  const [refreshing, setRefreshing] = useState<Record<string, boolean>>({})
  // Draft model-slug catalogue for a 'manual' provider while its config form is
  // open, plus the slug currently being typed into the add field. Seeded from
  // provider.models when the editor mounts.
  const [draftModels, setDraftModels] = useState<Record<string, string[]>>({})
  const [newModel, setNewModel] = useState<Record<string, string>>({})

  // The provider-key change waiting on a re-auth token, and whether the dialog
  // is open. PUT /api/v1/providers/{id} is re-auth gated post-onboarding
  // (Spec-6 FR-12.2 / FR-6.6); the token is replayed via configureProvider's
  // header arg.
  const [pending, setPending] = useState<PendingProviderChange | null>(null)
  const [reauthOpen, setReauthOpen] = useState(false)

  const { data: providers = [], isLoading, isError: providersError } = useQuery({
    queryKey: ['providers'],
    queryFn: fetchProviders,
  })

  const { mutate: applyChange, isPending: isSaving } = useMutation({
    mutationFn: ({ id, key, token, models }: { id: string; key: string; token: string; models?: string[] }) =>
      // An empty key means "don't change the key" (manual-models-only save), so
      // pass undefined for api_key in that case to leave the stored key intact.
      configureProvider(id, key === '' ? undefined : key, undefined, undefined, token, models),
    onSuccess: (_, { id }) => {
      queryClient.invalidateQueries({ queryKey: ['providers'] })
      addToast({ message: 'Provider saved', variant: 'success' })
      setExpandedProvider(null)
      setPending(null)
      setApiKeys((prev) => ({ ...prev, [id]: '' }))
    },
    onError: (err: Error) => {
      addToast({ message: isApiError(err) ? err.userMessage : err.message, variant: 'error' })
      setPending(null)
    },
  })

  // requestChange stages the edit then opens the re-auth dialog. The actual PUT
  // fires from onReAuthConfirmed once the consent token is minted — mirroring
  // IntegrationsSection's gated-save flow. `models` is sent only for manual
  // providers (undefined leaves the catalogue unchanged).
  const requestChange = (id: string, key: string, models?: string[]) => {
    setPending({ id, key, models })
    setReauthOpen(true)
  }

  const onReAuthConfirmed = (token: string) => {
    if (!pending) return
    applyChange({ id: pending.id, key: pending.key, token, models: pending.models })
  }

  // refreshLiveModels re-fetches a 'live' provider's upstream catalogue via the
  // dedicated refresh-models endpoint and updates the providers cache.
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
    try {
      const result = await testProvider(id)
      if (result.success) {
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
            <div key={i} className="h-14 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] animate-pulse" />
          ))}
        </div>
      ) : (
        <div className="space-y-2">
          {providers.map((provider) => {
            const hint = PROVIDER_HINTS[provider.id]
            const displayName = provider.display_name ?? provider.name ?? provider.id
            const isExpanded = expandedProvider === provider.id
            const connected = provider.status === 'connected'
            // UAT model-catalog: a 'live' provider exposes an upstream /models
            // endpoint (the list is fetched + refreshable); a 'manual' provider
            // has no endpoint, so its catalogue is the user-curated slug list.
            const catalogMode = providerCatalogMode(provider)

            return (
              <div
                key={provider.id}
                className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
              >
                {/* Provider row */}
                <div className="flex items-center gap-3 px-4 py-3">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium text-[var(--color-secondary)]">
                        {displayName}
                      </span>
                      {connected ? (
                        <Badge data-testid="connected-badge" variant="success" className="gap-1">
                          <CheckCircle size={10} weight="fill" /> Connected
                        </Badge>
                      ) : provider.status === 'error' ? (
                        <Badge variant="error" className="gap-1">
                          <XCircle size={10} weight="fill" /> Error
                        </Badge>
                      ) : (
                        <Badge variant="muted">Not configured</Badge>
                      )}
                      {connected && (
                        <Badge variant="muted" className="font-normal">
                          {catalogMode === 'live' ? 'Live model list' : 'Manual models'}
                        </Badge>
                      )}
                    </div>
                    {provider.models && provider.models.length > 0 && (
                      <p className="text-[10px] text-[var(--color-muted)] mt-0.5 font-mono">
                        {provider.models.slice(0, 3).join(', ')}{provider.models.length > 3 ? ` +${provider.models.length - 3}` : ''}
                      </p>
                    )}
                    {provider.error && (
                      <p className="text-[10px] text-[var(--color-error)] mt-0.5">{provider.error}</p>
                    )}
                  </div>

                  <div className="flex items-center gap-2 shrink-0">
                    {connected && catalogMode === 'live' && (
                      <button
                        type="button"
                        onClick={() => handleRefreshModels(provider.id)}
                        disabled={refreshing[provider.id]}
                        title="Re-fetch the live model list from the provider"
                        data-testid={`refresh-models-${provider.id}`}
                        className="text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors disabled:opacity-50"
                      >
                        {refreshing[provider.id] ? (
                          <ArrowCounterClockwise size={12} className="animate-spin inline" />
                        ) : 'Refresh models'}
                      </button>
                    )}
                    {connected && (
                      <button
                        type="button"
                        onClick={() => handleTest(provider.id)}
                        disabled={testing[provider.id]}
                        title="Re-test the connection"
                        className="text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors disabled:opacity-50"
                      >
                        {testing[provider.id] ? (
                          <ArrowCounterClockwise size={12} className="animate-spin inline" />
                        ) : 'Test'}
                      </button>
                    )}
                    <Button
                      size="sm"
                      onClick={() =>
                        setExpandedProvider(isExpanded ? null : provider.id)
                      }
                      className="h-7 px-3 text-xs"
                    >
                      {connected ? 'Edit' : (
                        <><Plus size={11} /> Configure</>
                      )}
                    </Button>
                  </div>
                </div>

                {/* Expanded config form */}
                {isExpanded && (
                  <div className="border-t border-[var(--color-border)] px-4 py-4 space-y-3 bg-[var(--color-surface-2)]">
                    <div>
                      <label className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">
                        API Key
                      </label>
                      <div className="relative">
                        <Input
                          type={showKey[provider.id] ? 'text' : 'password'}
                          value={apiKeys[provider.id] ?? ''}
                          onChange={(e) =>
                            setApiKeys((prev) => ({ ...prev, [provider.id]: e.target.value }))
                          }
                          placeholder={hint}
                          className="pr-9 font-mono text-xs"
                          autoComplete="off"
                        />
                        <button
                          type="button"
                          onClick={() =>
                            setShowKey((prev) => ({ ...prev, [provider.id]: !prev[provider.id] }))
                          }
                          className="absolute right-2.5 top-1/2 -translate-y-1/2 text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
                          aria-label={showKey[provider.id] ? 'Hide API key' : 'Show API key'}
                        >
                          {showKey[provider.id] ? <EyeSlash size={14} /> : <Eye size={14} />}
                        </button>
                      </div>
                    </div>

                    {/* Manual model-slug editor — only for endpoint-less
                        ('manual') providers. The list IS the provider's
                        catalogue; it constrains every model picker. */}
                    {catalogMode === 'manual' && (() => {
                      const models = draftModels[provider.id] ?? provider.models ?? []
                      const pendingSlug = (newModel[provider.id] ?? '').trim()
                      const addSlug = () => {
                        if (!pendingSlug || models.includes(pendingSlug)) return
                        setDraftModels((prev) => ({
                          ...prev,
                          [provider.id]: [...models, pendingSlug],
                        }))
                        setNewModel((prev) => ({ ...prev, [provider.id]: '' }))
                      }
                      const removeSlug = (slug: string) => {
                        setDraftModels((prev) => ({
                          ...prev,
                          [provider.id]: models.filter((m) => m !== slug),
                        }))
                      }
                      return (
                        <div>
                          <label className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">
                            Models
                          </label>
                          <p className="text-[10px] text-[var(--color-muted)] mb-2">
                            This provider has no live model list — add the model slugs you want available in the picker.
                          </p>
                          {models.length > 0 ? (
                            <ul className="flex flex-wrap gap-1.5 mb-2" data-testid={`model-list-${provider.id}`}>
                              {models.map((slug) => (
                                <li key={slug}>
                                  <Badge variant="muted" className="gap-1 font-mono">
                                    {slug}
                                    <button
                                      type="button"
                                      onClick={() => removeSlug(slug)}
                                      aria-label={`Remove ${slug}`}
                                      data-testid={`remove-model-${provider.id}-${slug}`}
                                      className="text-[var(--color-muted)] hover:text-[var(--color-error)]"
                                    >
                                      <X size={10} weight="bold" />
                                    </button>
                                  </Badge>
                                </li>
                              ))}
                            </ul>
                          ) : (
                            <p className="text-[10px] text-[var(--color-muted)] mb-2 italic">
                              No models added yet.
                            </p>
                          )}
                          <div className="flex gap-2">
                            <Input
                              value={newModel[provider.id] ?? ''}
                              onChange={(e) =>
                                setNewModel((prev) => ({ ...prev, [provider.id]: e.target.value }))
                              }
                              onKeyDown={(e) => {
                                if (e.key === 'Enter') {
                                  e.preventDefault()
                                  addSlug()
                                }
                              }}
                              placeholder="e.g. llama-3.1-70b"
                              className="font-mono text-xs"
                              data-testid={`add-model-input-${provider.id}`}
                            />
                            <Button
                              variant="outline"
                              size="sm"
                              type="button"
                              onClick={addSlug}
                              disabled={!pendingSlug || models.includes(pendingSlug)}
                              data-testid={`add-model-${provider.id}`}
                            >
                              <Plus size={11} /> Add
                            </Button>
                          </div>
                        </div>
                    )})()}

                    <div className="flex justify-end gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          setExpandedProvider(null)
                          setDraftModels((prev) => {
                            const { [provider.id]: _drop, ...rest } = prev
                            return rest
                          })
                          setNewModel((prev) => ({ ...prev, [provider.id]: '' }))
                        }}
                      >
                        Cancel
                      </Button>
                      <Button
                        size="sm"
                        onClick={() => {
                          const key = (apiKeys[provider.id] ?? '').trim()
                          // For manual providers, send the (possibly edited)
                          // slug catalogue; for live providers leave it untouched.
                          const models =
                            catalogMode === 'manual'
                              ? (draftModels[provider.id] ?? provider.models ?? [])
                              : undefined
                          requestChange(provider.id, key, models)
                        }}
                        disabled={isSaving || (() => {
                          const keyChanged = !!apiKeys[provider.id]?.trim()
                          if (catalogMode !== 'manual') return !keyChanged
                          // Manual: allow saving when the key changed OR the slug
                          // list was edited (a draft exists for this provider).
                          const modelsChanged = draftModels[provider.id] !== undefined
                          return !keyChanged && !modelsChanged
                        })()}
                        data-testid={`save-provider-${provider.id}`}
                      >
                        {catalogMode === 'manual' ? 'Save' : 'Save & Connect'}
                      </Button>
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

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
