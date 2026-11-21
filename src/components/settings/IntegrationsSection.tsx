import { useRef, useState } from 'react'
import { ReadErrorCallout } from './ReadErrorCallout'
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
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ErrorState } from '@/components/ui/error-state'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import {
  fetchIntegrationProviders,
  configureIntegrationProvider,
  getErrorMessage,
  type IntegrationProvider,
  type IntegrationProviderUpdateRequest,
} from '@/lib/api'
import { isApiError, parseServerErrorField } from '@/lib/api-error'
import { useUiStore } from '@/store/ui'
import { isReAuthCancelled } from './useReAuthGate'
import { useStepUp, type StepUpDescribeChange } from './useStepUp'
import { useSearchConnectionChecks } from './useSearchConnectionChecks'
import { WebSearchGroup, orderSearchProviders, KEY_NOT_REACHING_SEARCH_LABEL } from './WebSearchGroup'
import { FormRow, usePrimaryVariant } from './SaveBar'
import { useSectionSavePart } from './SectionSaveScope'
import { toWriteResponse, checkFormPart, type PartCheck } from './saveOutcome'
import type { FormSavePart } from './useFormSaveBar'

export function IntegrationsSection() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const stepUp = useStepUp()
  const connectionChecks = useSearchConnectionChecks()

  const [expanded, setExpanded] = useState<string | null>(null)
  const [apiKeys, setApiKeys] = useState<Record<string, string>>({})
  const [showKey, setShowKey] = useState<Record<string, boolean>>({})
  const [pendingChange, setPendingChange] = useState<{ id: string; removing: boolean } | null>(null)
  const [removalConfirmation, setRemovalConfirmation] = useState<{
    provider: IntegrationProvider
    describe: StepUpDescribeChange
  } | null>(null)
  // A single step-up gate cannot hold two changes. Lock synchronously, before
  // React commits the disabled controls, including the local pre-confirmation.
  const changePhase = useRef<'confirm-removal' | 'step-up' | null>(null)

  const {
    data,
    isLoading,
    isError,
    error: integrationsError,
    refetch: refetchIntegrations,
    isFetching: integrationsFetching,
  } = useQuery({
    queryKey: ['integrations'],
    queryFn: fetchIntegrationProviders,
  })

  // Settings redesign #1519 (FR-025, FR-053): Active transcriber is a draft
  // on the Providers Save bar. Its write is password-protected
  // (rest_integrations_roles.go requires re-auth), so it carries the Save's
  // one password prompt.
  const savedTranscriber = data?.voice.find((p) => p.active)?.id ?? ''
  const [transcriberDraft, setTranscriberDraft] = useState<string | null>(null)
  const transcriberChanged = transcriberDraft !== null && transcriberDraft !== savedTranscriber
  const shownTranscriber = transcriberChanged ? (transcriberDraft as string) : savedTranscriber
  const sentTranscriber = useRef<string | null>(null)
  const sentTranscriberBefore = useRef<string>('')
  const transcriberPart: FormSavePart = {
    id: 'active-transcriber',
    count: transcriberChanged ? 1 : 0,
    invalidCount: 0,
    passwordLabels: transcriberChanged ? ['Active transcriber'] : [],
    write: ({ reauthToken }) => {
      const id = transcriberDraft as string
      sentTranscriber.current = id
      sentTranscriberBefore.current = savedTranscriber
      return toWriteResponse(() => configureIntegrationProvider(id, { kind: 'voice', active: true }, reauthToken))
    },
    // C-6: this writer does not record failed applies, so a read equal to
    // the old value never proves "not saved"; it stays unknown.
    check: async (): Promise<PartCheck> => {
      if (sentTranscriber.current === null) return { kind: 'unknown' }
      let fresh: Awaited<ReturnType<typeof fetchIntegrationProviders>> | null = null
      const result = await checkFormPart(
        [{ key: 'integrations.active_transcriber', label: 'Active transcriber', draft: sentTranscriber.current, before: sentTranscriberBefore.current, recorded: false }],
        async () => {
          fresh = await fetchIntegrationProviders()
          return fresh.voice.find((p) => p.active)?.id ?? ''
        },
      )
      // Saved or changed elsewhere: the read value becomes the saved value.
      if ((result.kind === 'saved' || result.kind === 'changed-elsewhere') && fresh) queryClient.setQueryData(['integrations'], fresh)
      return result
    },
    onOutcome: (outcome) => {
      if (outcome.kind !== 'saved' && outcome.kind !== 'saved-not-in-use') return
      setTranscriberDraft(null)
      void queryClient.invalidateQueries({ queryKey: ['integrations'] })
    },
    discard: () => setTranscriberDraft(null),
  }
  const { outcome: transcriberOutcome, ownBar } = useSectionSavePart(transcriberPart)

  const { mutateAsync: applyChange, isPending: isSaving } = useMutation({
    mutationFn: ({ id, body, token }: { id: string; body: IntegrationProviderUpdateRequest; token?: string }) =>
      configureIntegrationProvider(id, body, token),
    // A failed sensitive write can have persisted partial changes. Replaying
    // the same single-use password consent must not retry that write.
    retry: false,
    onSuccess: async (_result, { id, body }) => {
      await queryClient.invalidateQueries({ queryKey: ['integrations'] })
      const name = data?.search.find((provider) => provider.id === id)?.display_name ?? id
      addToast({
        message: body.clear_api_key ? `${name} key removed.` : 'Integration updated',
        variant: 'success',
      })
      setExpanded(null)
      setApiKeys({})
    },
    // A failed save/removal can still have persisted state (ADR-096 FR-033).
    // Refresh on either outcome; performChange surfaces the actual error.
    onError: () => queryClient.invalidateQueries({ queryKey: ['integrations'] }),
  })

  const performChange = async (
    id: string,
    body: IntegrationProviderUpdateRequest,
    describe: StepUpDescribeChange,
  ) => {
    try {
      await stepUp.gate((token) => {
        if (body.api_key !== undefined || body.clear_api_key) connectionChecks.clear(id)
        return applyChange({ id, body, token })
      }, describe)
    } catch (err) {
      if (!isReAuthCancelled(err)) {
        const serverReason = isApiError(err) ? parseServerErrorField(err.body) : undefined
        addToast({
          message: serverReason ?? getErrorMessage(err, 'Integration update failed'),
          variant: 'error',
        })
      }
    } finally {
      changePhase.current = null
      setPendingChange(null)
    }
  }

  // Every sensitive write still uses the existing edition-specific gate.
  const requestChange = (id: string, body: IntegrationProviderUpdateRequest) => {
    if (changePhase.current || connectionChecks.view(id).pending) return
    changePhase.current = 'step-up'
    setPendingChange({ id, removing: false })
    void performChange(id, body, {
      title: 'Update this integration?',
      body: 'API keys are stored encrypted, and provider roles decide which provider Omnipus uses for this kind of work.',
      confirmLabel: 'Update integration',
    })
  }

  const requestRemoval = (provider: IntegrationProvider) => {
    if (changePhase.current || connectionChecks.view(provider.id).pending) return
    if (!provider.requires_key || !provider.configured) return
    const role = data?.default_search === provider.id
      ? 'default'
      : data?.fallback_search === provider.id ? 'fallback' : undefined
    const describe: StepUpDescribeChange = {
      title: `Remove the ${provider.display_name} key?`,
      body: `This deletes the saved key from Omnipus and switches off ${provider.display_name}. It does not revoke the key with ${provider.display_name}.` +
        (role ? ` ${provider.display_name} will remain your ${role} search service. Omnipus will not choose a replacement. Add a key or change that choice to fix it.` : ''),
      confirmLabel: 'Remove key',
    }
    setPendingChange({ id: provider.id, removing: true })
    if (stepUp.mode === 'password') {
      // Local removal explains the destructive outcome before the password
      // gate. Platform mode uses that gate's own confirmation, never two.
      changePhase.current = 'confirm-removal'
      setRemovalConfirmation({ provider, describe })
    } else {
      changePhase.current = 'step-up'
      void performChange(provider.id, { kind: 'search', clear_api_key: true }, describe)
    }
  }

  const acceptLocalRemoval = () => {
    if (changePhase.current !== 'confirm-removal' || !removalConfirmation) return
    changePhase.current = 'step-up'
    setRemovalConfirmation(null)
    void performChange(
      removalConfirmation.provider.id,
      { kind: 'search', clear_api_key: true },
      removalConfirmation.describe,
    )
  }

  const cancelLocalRemoval = (open: boolean) => {
    if (open || changePhase.current !== 'confirm-removal') return
    changePhase.current = null
    setRemovalConfirmation(null)
    setPendingChange(null)
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
    const check = connectionChecks.view(p.id)
    const rowBusy = isSaving || pendingChange !== null || stepUp.open || check.pending
    const removing = isSaving && pendingChange?.id === p.id && pendingChange.removing
    const canCheck = p.configured && p.usable === true

    return (
      <Card
        key={p.id}
        className="overflow-hidden"
        data-testid={`search-row-${p.id}`}
      >
        <div className="flex flex-col items-start sm:flex-row sm:items-center gap-[var(--space-2-5)] px-[var(--space-3)] py-[var(--space-2-5)]">
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

          <div className="flex flex-wrap items-center gap-[var(--space-2)] shrink-0">
            {p.requires_key && (
              <>
                <Button
                  size="sm"
                  variant="secondary"
                  className="h-7 px-[var(--space-2-5)] text-[length:var(--type-utility-xs-size)]"
                  onClick={() => setExpanded(isExpanded ? null : p.id)}
                  disabled={rowBusy}
                  data-testid={`addkey-${p.id}`}
                >
                  {p.configured ? 'Edit key' : (
                    <><Plus size={11} /> Add key</>
                  )}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="px-[var(--space-2-5)] text-[length:var(--type-utility-xs-size)]"
                  onClick={() => {
                    if (!changePhase.current) void connectionChecks.run(p.id)
                  }}
                  disabled={rowBusy || !canCheck || check.cooldown}
                  actionState={check.pending ? 'pending' : 'idle'}
                  aria-describedby={!canCheck ? `check-help-${p.id}` : undefined}
                >
                  Check connection
                </Button>
                {p.configured && (
                  <Button
                    size="sm"
                    variant="ghost"
                    className="px-[var(--space-2-5)] text-[length:var(--type-utility-xs-size)]"
                    onClick={() => requestRemoval(p)}
                    disabled={rowBusy}
                    actionState={removing ? 'pending' : 'idle'}
                  >
                    {removing ? 'Removing…' : 'Remove key'}
                  </Button>
                )}
              </>
            )}
          </div>
        </div>

        {p.requires_key && (
          <div className="px-[var(--space-3)] pb-[var(--space-2-5)] text-[length:var(--type-utility-xs-size)]">
            {!canCheck && (
              <p id={`check-help-${p.id}`} className="text-[var(--color-muted)]">
                Add or save a key before checking.
              </p>
            )}
            {check.pending ? (
              <p role="status" className="text-[var(--color-secondary)]">Checking connection…</p>
            ) : (
              <>
                {check.message && <p role="status" className="text-[var(--color-secondary)]">{check.message}</p>}
                {check.error && <ErrorState message={check.error} className="items-start py-[var(--space-0)]" />}
                {check.cooldownMessage && <p role="status" className="text-[var(--color-muted)]">{check.cooldownMessage}</p>}
              </>
            )}
          </div>
        )}

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
                variant={primaryVariant}
                size="sm"
                onClick={() =>
                  requestChange(
                    p.id,
                    rolesOnWire
                      ? { kind: p.kind, api_key: keyVal.trim() }
                      : { kind: p.kind, api_key: keyVal.trim(), active: true },
                  )
                }
                disabled={!keyVal.trim() || rowBusy}
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

  // Voice rows (Settings redesign #1519, FR-053): which service transcribes
  // is the Active transcriber choice above; each row only shows its state
  // and opens its key in the side panel below.
  const voiceKeyProvider = data?.voice.find((p) => p.id === expanded && p.requires_key)
  const primaryVariant = usePrimaryVariant()

  const renderVoiceProvider = (p: IntegrationProvider) => {
    const isExpanded = expanded === p.id

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
            {p.requires_key && (
              <Button
                size="sm"
                variant="outline"
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
      </Card>
    )
  }

  return (
    <div className="space-y-[var(--space-4)]">
      <div>
        <h3 className="font-headline font-bold text-base text-[var(--color-secondary)]">Integrations</h3>
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
          Configure the web-search and voice-input providers your agents use. API keys are stored
          encrypted; changes require re-typing your password.
        </p>
      </div>

      {isError && (
        <ReadErrorCallout
          title="Failed to load integrations"
          error={integrationsError}
          onRetry={() => void refetchIntegrations()}
          retrying={integrationsFetching}
          data-testid="integrations-load-error"
        />
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
                  saving={isSaving || pendingChange !== null || stepUp.open || connectionChecks.anyPending}
                  onSetDefault={onSetDefault}
                  onSetFallback={onSetFallback}
                  onSetNoFallback={onSetNoFallback}
                  onFixProvider={(id) => {
                    if (!changePhase.current && !connectionChecks.view(id).pending) setExpanded(id)
                  }}
                />
                <div className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] space-y-[var(--space-1)]">
                  <p>Runs one small search with your saved key. Your service may charge for it.</p>
                  <p>Ready means the key is loaded. Check connection tests it with the service.</p>
                </div>
                {orderSearchProviders(data.search).map(renderSearchRow)}
              </>
            )}
          </section>

          <section className="space-y-[var(--space-2)]">
            <div className="flex items-center gap-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-semibold uppercase tracking-wide text-[var(--color-muted)]">
              <Microphone size={13} weight="bold" /> Voice input
            </div>
            {data.voice.length === 0 ? (
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">No voice providers available.</p>
            ) : (
              <>
                <FormRow
                  label="Active transcriber"
                  description="Turns your voice messages into text. One service at a time."
                  password
                  fieldId="settings-providers-active-transcriber"
                >
                  {({ id, describedBy, fieldId }) =>
                    data.voice.some((p) => p.configured) ? (
                      <select
                        tabIndex={0}
                        id={id}
                        data-settings-field={fieldId}
                        data-testid="active-transcriber"
                        value={shownTranscriber}
                        onChange={(e) => setTranscriberDraft(e.target.value)}
                        aria-describedby={describedBy}
                        className="flex h-9 w-full max-w-xs rounded-md border border-[var(--color-border)] bg-transparent px-[var(--space-3)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]"
                      >
                        {savedTranscriber === '' && <option value="">None</option>}
                        {data.voice
                          .filter((p) => p.configured)
                          .map((p) => (
                            <option key={p.id} value={p.id}>
                              {p.display_name}
                            </option>
                          ))}
                      </select>
                    ) : (
                      <p id={id} className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
                        Add a key to a voice service below first.
                      </p>
                    )
                  }
                </FormRow>
                {transcriberOutcome}
                {data.voice.map(renderVoiceProvider)}
              </>
            )}
          </section>
        </>
      ) : null}

      <ConfirmDialog
        open={removalConfirmation !== null}
        onOpenChange={cancelLocalRemoval}
        title={removalConfirmation?.describe.title ?? ''}
        description={removalConfirmation?.describe.body ?? ''}
        confirmLabel="Remove key"
        onConfirm={acceptLocalRemoval}
        destructive
      />
      {/* FR-053: a voice service's key opens in a side panel with the key
          field and show/hide only (no Test, no Remove). The password prompt
          opens over it; Cancel there closes only the prompt. */}
      <Sheet
        open={voiceKeyProvider !== undefined}
        onOpenChange={(open) => {
          if (!open && !stepUp.open) setExpanded(null)
        }}
      >
        <SheetContent
          side="right"
          widthClass="w-[90vw] sm:max-w-lg"
          className="p-0"
          data-testid="voice-key-sheet"
          aria-hidden={stepUp.open ? true : undefined}
          inert={stepUp.open ? true : undefined}
          onPointerDownOutside={(e) => {
            if (stepUp.open) e.preventDefault()
          }}
        >
          {voiceKeyProvider && (
            <>
              <SheetHeader className="px-[var(--space-4)] pr-[var(--space-7)]">
                <SheetTitle>{voiceKeyProvider.display_name} key</SheetTitle>
              </SheetHeader>
              <SheetDescription className="px-[var(--space-4)] pt-[var(--space-2)]">
                Stored encrypted on this server. Saving the key asks for your password.
              </SheetDescription>
              <div className="px-[var(--space-4)] pt-[var(--space-3)] space-y-[var(--space-3)]">
                <div>
                  <Label htmlFor={`key-input-${voiceKeyProvider.id}`} className="mb-[var(--space-2)] block">API key</Label>
                  <div className="relative">
                    <Input
                      id={`key-input-${voiceKeyProvider.id}`}
                      type={showKey[voiceKeyProvider.id] ? 'text' : 'password'}
                      value={apiKeys[voiceKeyProvider.id] ?? ''}
                      onChange={(e) => setApiKeys((prev) => ({ ...prev, [voiceKeyProvider.id]: e.target.value }))}
                      placeholder={`${voiceKeyProvider.display_name} API key`}
                      className="pr-[var(--space-5)] font-mono text-[length:var(--type-utility-xs-size)]"
                      autoComplete="off"
                      data-testid={`key-input-${voiceKeyProvider.id}`}
                    />
                    <IconButton
                      variant="ghost"
                      type="button"
                      onClick={() => setShowKey((prev) => ({ ...prev, [voiceKeyProvider.id]: !prev[voiceKeyProvider.id] }))}
                      className="absolute right-2.5 top-1/2 h-auto w-auto -translate-y-1/2 p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
                      aria-label={showKey[voiceKeyProvider.id] ? 'Hide API key' : 'Show API key'}
                    >
                      {showKey[voiceKeyProvider.id] ? <EyeSlash size={14} /> : <Eye size={14} />}
                    </IconButton>
                  </div>
                </div>
                <div className="flex justify-end gap-[var(--space-2)]">
                  <Button variant="outline" size="sm" onClick={() => setExpanded(null)}>
                    Cancel
                  </Button>
                  <Button
                    size="sm"
                    variant={primaryVariant}
                    onClick={() =>
                      requestChange(voiceKeyProvider.id, {
                        kind: voiceKeyProvider.kind,
                        api_key: (apiKeys[voiceKeyProvider.id] ?? '').trim(),
                      })
                    }
                    disabled={!(apiKeys[voiceKeyProvider.id] ?? '').trim() || isSaving}
                    data-testid={`save-${voiceKeyProvider.id}`}
                  >
                    Save key
                  </Button>
                </div>
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>

      {stepUp.dialogs}
      {ownBar}
    </div>
  )
}
