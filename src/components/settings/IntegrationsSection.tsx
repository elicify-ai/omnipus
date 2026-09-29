import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  CheckCircle,
  Eye,
  EyeSlash,
  Plus,
  MagnifyingGlass,
  Microphone,
  Star,
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import {
  fetchIntegrationProviders,
  configureIntegrationProvider,
  getErrorMessage,
  type IntegrationProvider,
  type IntegrationProviderUpdateRequest,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { isReAuthCancelled } from './useReAuthGate'
import { useStepUp } from './useStepUp'
import { WebSearchGroup, orderSearchProviders, KEY_NOT_REACHING_SEARCH_LABEL } from './WebSearchGroup'

export function IntegrationsSection() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const stepUp = useStepUp()

  const [expanded, setExpanded] = useState<string | null>(null)
  const [apiKeys, setApiKeys] = useState<Record<string, string>>({})
  const [showKey, setShowKey] = useState<Record<string, boolean>>({})

  const {
    data,
    isLoading,
    isError,
  } = useQuery({
    queryKey: ['integrations'],
    queryFn: fetchIntegrationProviders,
  })

  const { mutateAsync: applyChange, isPending: isSaving } = useMutation({
    mutationFn: ({ id, body, token }: { id: string; body: IntegrationProviderUpdateRequest; token?: string }) =>
      configureIntegrationProvider(id, body, token),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['integrations'] })
      addToast({ message: 'Integration updated', variant: 'success' })
      setExpanded(null)
      setApiKeys({})
    },
    onError: (err: Error) => {
      // A failed save can still have persisted state: the gateway stores the
      // credential and writes config.json BEFORE the reload (or the
      // post-reload usability judgment) fails — the persisted write stays
      // (ADR-096 FR-033). Invalidate so the list reflects what the backend
      // actually holds instead of the stale pre-save cache.
      queryClient.invalidateQueries({ queryKey: ['integrations'] })
      addToast({
        message: getErrorMessage(err, 'Integration update failed'),
        variant: 'error',
      })
    },
  })

  // requestChange runs the edit through the step-up gate (ADR-0010 WP3):
  // ReAuthDialog + a replayed consent token in local mode, ConfirmDialog with
  // no token in platform mode. Either way the PUT only fires once the
  // operator stands behind it.
  const requestChange = (id: string, body: IntegrationProviderUpdateRequest) => {
    void stepUp
      .gate(
        (token) => applyChange({ id, body, token }),
        {
          title: 'Update this integration?',
          body: 'API keys are stored encrypted, and provider roles decide which provider Omnipus uses for this kind of work.',
          confirmLabel: 'Update integration',
        },
      )
      .catch((err) => {
        // A dismissed dialog is a no-op, not a failure. A real save failure
        // already surfaced its toast via the mutation's onError above.
        if (isReAuthCancelled(err)) return
      })
  }

  // Search rows: #1055 — ONE row per provider, status only; role assignment
  // moved to the Default search / Fallback cards' "Change" selector
  // (WebSearchGroup). Role badges derive from the response-level
  // resolved roles (default_search / fallback_search), not from the row's
  // own `active`/`fallback` flags — the response fields are what R5's
  // healing has already applied (an ignored fallback reads
  // fallback_search:null with a reason), so a row flag would lie exactly
  // where healing kicked in. Readiness is reported honestly: "Ready" only
  // when the tool's own test passes; a stored key whose resolved value is
  // empty reads "Key not reaching search" — configured (the secret is in
  // the vault) is not the badge test (FR-028). There is no per-row "Set
  // active" button. When default_search is on the wire, a key save carries
  // api_key only — storing a key is separable from assigning a role (spec
  // § Contract shape). A payload without that field is still the pre-role
  // screen, whose save is "Save & activate".
  const onSetDefault = (id: string) => requestChange(id, { kind: 'search', active: true })
  const onSetFallback = (id: string) => requestChange(id, { kind: 'search', fallback: true })
  const onSetNoFallback = () => {
    // The contract clears the fallback "regardless of the addressed id"; the PUT still needs a real search id in
    // its path, so anchor it to the default (or first row).
    const anchor = data?.default_search ?? data?.search[0]?.id
    if (anchor) requestChange(anchor, { kind: 'search', fallback: false })
  }
  const renderSearchRow = (p: IntegrationProvider) => {
    const isExpanded = expanded === p.id
    const keyVal = apiKeys[p.id] ?? ''
    const rolesOnWire = data?.default_search !== undefined
    const isDefaultRow = data?.default_search === p.id
    const isFallbackRow = data?.fallback_search === p.id

    return (
      <Card
        key={p.id}
        className="overflow-hidden"
        data-testid={`search-row-${p.id}`}
      >
        <div className="flex items-center gap-[var(--space-2-5)] px-[var(--space-3)] py-[var(--space-2-5)]">
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-[var(--space-2)] flex-wrap">
              <span className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">{p.display_name}</span>
              {isDefaultRow && (
                <Badge data-testid={`badge-default-${p.id}`} variant="success" className="gap-[var(--space-1)]">
                  <Star size={10} weight="fill" /> Default
                </Badge>
              )}
              {isFallbackRow && (
                <Badge data-testid={`badge-fallback-${p.id}`} variant="secondary" className="gap-[var(--space-1)]">
                  Fallback{p.fallback_automatic ? ' (automatic)' : ''}
                </Badge>
              )}
              {/* Spec § Settings screen "Badge": "Active" goes away for search
                  rows, replaced by "Default". The pre-role payload has no
                  default_search, so the provider it marks `active` is that
                  default. The word is "Default"; the testid stays `active-*`
                  because the pre-ADR section test locks that marker. A
                  role-bearing response never takes this branch. */}
              {!rolesOnWire && p.active && (
                <Badge data-testid={`active-${p.id}`} variant="success" className="gap-[var(--space-1)]">
                  <Star size={10} weight="fill" /> Default
                </Badge>
              )}
              {p.usable === true ? (
                <Badge data-testid={`ready-${p.id}`} variant="muted">Ready</Badge>
              ) : p.usable === false ? (
                p.configured && p.requires_key ? (
                  <Badge data-testid={`key-not-reaching-${p.id}`} variant="warning">{KEY_NOT_REACHING_SEARCH_LABEL}</Badge>
                ) : !p.configured && p.requires_key ? (
                  <Badge variant="muted">Needs API key</Badge>
                ) : (
                  <Badge data-testid={`needs-config-${p.id}`} variant="muted">Needs configuration</Badge>
                )
              ) : (
                // usable is absent on this row (pre-ADR-096 payload shape or
                // a shape the tool does not report); keep the original status
                // badges so the row is not statusless.
                p.configured ? (
                  <Badge variant="muted" className="gap-[var(--space-1)]">
                    <CheckCircle size={10} weight="fill" /> Configured
                  </Badge>
                ) : p.requires_key ? (
                  <Badge variant="muted">Needs API key</Badge>
                ) : (
                  <Badge data-testid={`needs-config-${p.id}`} variant="muted">Needs configuration</Badge>
                )
              )}
            </div>
          </div>

          <div className="flex items-center gap-[var(--space-2)] shrink-0">
            {p.requires_key && (
              <Button
                size="sm"
                variant="secondary"
                className="h-7 px-[var(--space-2-5)] text-[length:var(--type-utility-xs-size)]"
                onClick={() => setExpanded(isExpanded ? null : p.id)}
                data-testid={`addkey-${p.id}`}
              >
                {p.configured ? 'Edit key' : (
                  <><Plus size={11} /> Add key</>
                )}
              </Button>
            )}
          </div>
        </div>

        {isExpanded && p.requires_key && (
          <div className="border-t border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-3)] space-y-[var(--space-2-5)] bg-[var(--color-surface-2)]">
            <div>
              <Label htmlFor={`key-input-${p.id}`} className="mb-[var(--space-2)] block">API Key</Label>
              <div className="relative">
                <Input
                  id={`key-input-${p.id}`}
                  type={showKey[p.id] ? 'text' : 'password'}
                  value={keyVal}
                  onChange={(e) => setApiKeys((prev) => ({ ...prev, [p.id]: e.target.value }))}
                  placeholder={`${p.display_name} API key`}
                  className="pr-[var(--space-5)] font-mono text-[length:var(--type-utility-xs-size)]"
                  autoComplete="off"
                  data-testid={`key-input-${p.id}`}
                />
                <IconButton
                  variant="ghost"
                  type="button"
                  onClick={() => setShowKey((prev) => ({ ...prev, [p.id]: !prev[p.id] }))}
                  className="absolute right-2.5 top-1/2 h-auto w-auto -translate-y-1/2 p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
                  aria-label={showKey[p.id] ? 'Hide API key' : 'Show API key'}
                >
                  {showKey[p.id] ? <EyeSlash size={14} /> : <Eye size={14} />}
                </IconButton>
              </div>
              <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] mt-[var(--space-1)]">
                Stored encrypted (AES-256-GCM) — saving requires re-typing your password.
              </p>
            </div>
            <div className="flex justify-end gap-[var(--space-2)]">
              <Button variant="outline" size="sm" onClick={() => setExpanded(null)}>
                Cancel
              </Button>
              <Button
                size="sm"
                onClick={() =>
                  requestChange(
                    p.id,
                    rolesOnWire
                      ? { kind: p.kind, api_key: keyVal.trim() }
                      : { kind: p.kind, api_key: keyVal.trim(), active: true },
                  )
                }
                disabled={!keyVal.trim() || isSaving}
                data-testid={`save-${p.id}`}
              >
                {rolesOnWire ? 'Save key' : 'Save & activate'}
              </Button>
            </div>
          </div>
        )}
      </Card>
    )
  }

  // Voice rows: untouched by ADR-096 — the spec leaves voice integrations
  // alone (one active transcriber). The "Active" badge, the "Set active"
  // button and the coupled "Save & activate" keep their pre-ADR behaviour
  // and their pre-ADR testids, which the existing voice tests assert.
  const renderVoiceProvider = (p: IntegrationProvider) => {
    const isExpanded = expanded === p.id
    const keyVal = apiKeys[p.id] ?? ''

    return (
      <Card
        key={p.id}
        className="overflow-hidden"
      >
        <div className="flex items-center gap-[var(--space-2-5)] px-[var(--space-3)] py-[var(--space-2-5)]">
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-[var(--space-2)] flex-wrap">
              <span className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">{p.display_name}</span>
              {p.active && (
                <Badge data-testid={`active-${p.id}`} variant="success" className="gap-[var(--space-1)]">
                  <Star size={10} weight="fill" /> Active
                </Badge>
              )}
              {p.configured ? (
                <Badge variant="muted" className="gap-[var(--space-1)]">
                  <CheckCircle size={10} weight="fill" /> Configured
                </Badge>
              ) : p.requires_key ? (
                <Badge variant="muted">Needs API key</Badge>
              ) : (
                // Keyless, unconfigured (e.g. audio-model needing a
                // voice.model_name): previously fell through both branches
                // above and rendered no badge at all.
                <Badge data-testid={`needs-config-${p.id}`} variant="muted">
                  Needs configuration
                </Badge>
              )}
            </div>
          </div>

          <div className="flex items-center gap-[var(--space-2)] shrink-0">
            {p.configured && !p.active && (
              <Button
                size="sm"
                variant="outline"
                className="h-7 px-[var(--space-2-5)] text-[length:var(--type-utility-xs-size)]"
                onClick={() => requestChange(p.id, { kind: p.kind, active: true })}
                disabled={isSaving}
                data-testid={`activate-${p.id}`}
              >
                Set active
              </Button>
            )}
            {p.requires_key && (
              <Button
                size="sm"
                className="h-7 px-[var(--space-2-5)] text-[length:var(--type-utility-xs-size)]"
                onClick={() => setExpanded(isExpanded ? null : p.id)}
                data-testid={`addkey-${p.id}`}
              >
                {p.configured ? 'Edit key' : (
                  <><Plus size={11} /> Add key</>
                )}
              </Button>
            )}
          </div>
        </div>

        {isExpanded && p.requires_key && (
          <div className="border-t border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-3)] space-y-[var(--space-2-5)] bg-[var(--color-surface-2)]">
            <div>
              <Label htmlFor={`key-input-${p.id}`} className="mb-[var(--space-2)] block">API Key</Label>
              <div className="relative">
                <Input
                  id={`key-input-${p.id}`}
                  type={showKey[p.id] ? 'text' : 'password'}
                  value={keyVal}
                  onChange={(e) => setApiKeys((prev) => ({ ...prev, [p.id]: e.target.value }))}
                  placeholder={`${p.display_name} API key`}
                  className="pr-[var(--space-5)] font-mono text-[length:var(--type-utility-xs-size)]"
                  autoComplete="off"
                  data-testid={`key-input-${p.id}`}
                />
                <IconButton
                  variant="ghost"
                  type="button"
                  onClick={() => setShowKey((prev) => ({ ...prev, [p.id]: !prev[p.id] }))}
                  className="absolute right-2.5 top-1/2 h-auto w-auto -translate-y-1/2 p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
                  aria-label={showKey[p.id] ? 'Hide API key' : 'Show API key'}
                >
                  {showKey[p.id] ? <EyeSlash size={14} /> : <Eye size={14} />}
                </IconButton>
              </div>
              <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] mt-[var(--space-1)]">
                Stored encrypted (AES-256-GCM) — saving requires re-typing your password.
              </p>
            </div>
            <div className="flex justify-end gap-[var(--space-2)]">
              <Button variant="outline" size="sm" onClick={() => setExpanded(null)}>
                Cancel
              </Button>
              <Button
                size="sm"
                onClick={() =>
                  requestChange(p.id, { kind: p.kind, api_key: keyVal.trim(), active: true })
                }
                disabled={!keyVal.trim() || isSaving}
                data-testid={`save-${p.id}`}
              >
                Save &amp; activate
              </Button>
            </div>
          </div>
        )}
      </Card>
    )
  }

  return (
    <div className="space-y-[var(--space-4)]">
      <div>
        <h2 className="font-headline font-bold text-base text-[var(--color-secondary)]">Integrations</h2>
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
          Configure the web-search and voice-input providers your agents use. API keys are stored
          encrypted; changes require re-typing your password.
        </p>
      </div>

      {isError && (
        <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-text-error)]">Failed to load integrations. Please try again.</p>
      )}

      {isLoading ? (
        <div className="space-y-[var(--space-2)]">
          {[1, 2, 3].map((i) => (
            <Card
              key={i}
              className="h-14 animate-pulse"
            />
          ))}
        </div>
      ) : data ? (
        <>
          <section className="space-y-[var(--space-2)]">
            <div className="flex items-center gap-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-semibold uppercase tracking-wide text-[var(--color-muted)]">
              <MagnifyingGlass size={13} weight="bold" /> Web Search
            </div>
            {data.search.length === 0 ? (
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">No search providers available.</p>
            ) : (
              <>
                <WebSearchGroup
                  providers={data.search}
                  defaultSearch={data.default_search}
                  fallbackSearch={data.fallback_search}
                  fallbackIgnoredReason={data.fallback_ignored_reason}
                  nativeSearchInEffect={data.native_search_in_effect}
                  saving={isSaving}
                  onSetDefault={onSetDefault}
                  onSetFallback={onSetFallback}
                  onSetNoFallback={onSetNoFallback}
                  onFixProvider={(id) => setExpanded(id)}
                />
                {orderSearchProviders(data.search).map(renderSearchRow)}
              </>
            )}
          </section>

          <section className="space-y-[var(--space-2)]">
            <div className="flex items-center gap-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-semibold uppercase tracking-wide text-[var(--color-muted)]">
              <Microphone size={13} weight="bold" /> Voice Input
            </div>
            {data.voice.length === 0 ? (
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">No voice providers available.</p>
            ) : (
              data.voice.map(renderVoiceProvider)
            )}
          </section>
        </>
      ) : null}

      {stepUp.dialogs}
    </div>
  )
}
