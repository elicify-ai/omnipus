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
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import {
  fetchIntegrationProviders,
  configureIntegrationProvider,
  getErrorMessage,
  type IntegrationProvider,
  type IntegrationProviderUpdateRequest,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { ReAuthDialog } from './ReAuthDialog'

// A pending edit captured before the re-auth prompt; replayed once the consent
// token is minted.
type PendingChange = {
  id: string
  body: IntegrationProviderUpdateRequest
}

export function IntegrationsSection() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [expanded, setExpanded] = useState<string | null>(null)
  const [apiKeys, setApiKeys] = useState<Record<string, string>>({})
  const [showKey, setShowKey] = useState<Record<string, boolean>>({})

  // The change waiting on a re-auth token, and whether the dialog is open.
  const [pending, setPending] = useState<PendingChange | null>(null)
  const [reauthOpen, setReauthOpen] = useState(false)

  const {
    data,
    isLoading,
    isError,
  } = useQuery({
    queryKey: ['integrations'],
    queryFn: fetchIntegrationProviders,
  })

  const { mutate: applyChange, isPending: isSaving } = useMutation({
    mutationFn: ({ id, body, token }: { id: string; body: IntegrationProviderUpdateRequest; token: string }) =>
      configureIntegrationProvider(id, body, token),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['integrations'] })
      addToast({ message: 'Integration updated', variant: 'success' })
      setExpanded(null)
      setPending(null)
      setApiKeys({})
    },
    onError: (err: Error) => {
      addToast({
        message: getErrorMessage(err, 'Integration update failed'),
        variant: 'error',
      })
      setPending(null)
    },
  })

  // requestChange stages the edit then opens the re-auth dialog. The actual PUT
  // fires from onReAuthConfirmed once the consent token is minted.
  const requestChange = (id: string, body: IntegrationProviderUpdateRequest) => {
    setPending({ id, body })
    setReauthOpen(true)
  }

  const onReAuthConfirmed = (token: string) => {
    if (!pending) return
    applyChange({ id: pending.id, body: pending.body, token })
  }

  const renderProvider = (p: IntegrationProvider) => {
    const isExpanded = expanded === p.id
    const keyVal = apiKeys[p.id] ?? ''

    return (
      <div
        key={p.id}
        className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
      >
        <div className="flex items-center gap-3 px-4 py-3">
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="text-sm font-medium text-[var(--color-secondary)]">{p.display_name}</span>
              {p.active && (
                <Badge data-testid={`active-${p.id}`} variant="success" className="gap-1">
                  <Star size={10} weight="fill" /> Active
                </Badge>
              )}
              {p.configured ? (
                <Badge variant="muted" className="gap-1">
                  <CheckCircle size={10} weight="fill" /> Configured
                </Badge>
              ) : p.requires_key ? (
                <Badge variant="muted">Needs API key</Badge>
              ) : null}
            </div>
          </div>

          <div className="flex items-center gap-2 shrink-0">
            {/* Activate — only when configured and not already active. */}
            {p.configured && !p.active && (
              <Button
                size="sm"
                variant="outline"
                className="h-7 px-3 text-xs"
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
                className="h-7 px-3 text-xs"
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
          <div className="border-t border-[var(--color-border)] px-4 py-4 space-y-3 bg-[var(--color-surface-2)]">
            <div>
              <label className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">API Key</label>
              <div className="relative">
                <Input
                  type={showKey[p.id] ? 'text' : 'password'}
                  value={keyVal}
                  onChange={(e) => setApiKeys((prev) => ({ ...prev, [p.id]: e.target.value }))}
                  placeholder={`${p.display_name} API key`}
                  className="pr-9 font-mono text-xs"
                  autoComplete="off"
                  data-testid={`key-input-${p.id}`}
                />
                <button
                  type="button"
                  onClick={() => setShowKey((prev) => ({ ...prev, [p.id]: !prev[p.id] }))}
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
                  aria-label={showKey[p.id] ? 'Hide API key' : 'Show API key'}
                >
                  {showKey[p.id] ? <EyeSlash size={14} /> : <Eye size={14} />}
                </button>
              </div>
              <p className="text-[10px] text-[var(--color-muted)] mt-1.5">
                Stored encrypted (AES-256-GCM) — saving requires re-typing your password.
              </p>
            </div>
            <div className="flex justify-end gap-2">
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
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="font-headline font-bold text-base text-[var(--color-secondary)]">Integrations</h2>
        <p className="text-xs text-[var(--color-muted)] mt-0.5">
          Configure the web-search and voice-input providers your agents use. API keys are stored
          encrypted; changes require re-typing your password.
        </p>
      </div>

      {isError && (
        <p className="text-sm text-red-400">Failed to load integrations. Please try again.</p>
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
      ) : data ? (
        <>
          <section className="space-y-2">
            <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-[var(--color-muted)]">
              <MagnifyingGlass size={13} weight="bold" /> Web Search
            </div>
            {data.search.length === 0 ? (
              <p className="text-xs text-[var(--color-muted)]">No search providers available.</p>
            ) : (
              data.search.map(renderProvider)
            )}
          </section>

          <section className="space-y-2">
            <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-[var(--color-muted)]">
              <Microphone size={13} weight="bold" /> Voice Input
            </div>
            {data.voice.length === 0 ? (
              <p className="text-xs text-[var(--color-muted)]">No voice providers available.</p>
            ) : (
              data.voice.map(renderProvider)
            )}
          </section>
        </>
      ) : null}

      <ReAuthDialog
        open={reauthOpen}
        onOpenChange={(o) => {
          setReauthOpen(o)
          if (!o) setPending(null)
        }}
        title="Confirm to update integration"
        description="Re-type your password to change this integration provider."
        onConfirmed={onReAuthConfirmed}
      />
    </div>
  )
}
