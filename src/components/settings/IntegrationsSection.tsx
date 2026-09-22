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
          body: 'The key is stored encrypted, and the provider you picked becomes the one Omnipus uses for this kind of work from now on.',
          confirmLabel: 'Update integration',
        },
      )
      .catch((err) => {
        // A dismissed dialog is a no-op, not a failure. A real save failure
        // already surfaced its toast via the mutation's onError above.
        if (isReAuthCancelled(err)) return
      })
  }

  const renderProvider = (p: IntegrationProvider) => {
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
                // D14 fix: a keyless provider (requires_key:false) that also
                // isn't configured yet — e.g. SearXNG (needs a base_url) or
                // audio-model (needs a voice.model_name) — previously fell
                // through both branches above and rendered NO badge at all,
                // leaving the row looking inert/unstatused next to every
                // other provider. There is no UI path to set SearXNG's
                // base_url today (no `base_url` field on
                // IntegrationProviderUpdateRequest), so this is status-only;
                // wiring an action is a separate, out-of-scope contract change.
                <Badge variant="muted" data-testid={`needs-config-${p.id}`}>
                  Needs configuration
                </Badge>
              )}
            </div>
          </div>

          <div className="flex items-center gap-[var(--space-2)] shrink-0">
            {/* Activate — only when configured and not already active. */}
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
              data.search.map(renderProvider)
            )}
          </section>

          <section className="space-y-[var(--space-2)]">
            <div className="flex items-center gap-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-semibold uppercase tracking-wide text-[var(--color-muted)]">
              <Microphone size={13} weight="bold" /> Voice Input
            </div>
            {data.voice.length === 0 ? (
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">No voice providers available.</p>
            ) : (
              data.voice.map(renderProvider)
            )}
          </section>
        </>
      ) : null}

      {stepUp.dialogs}
    </div>
  )
}
