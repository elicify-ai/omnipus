/**
 * PerformanceSection — Settings → Performance tab.
 *
 * Spec-3 max-parallel fan-out gate: lets an admin configure
 * max_parallel_agents (the global dispatch semaphore capacity).
 * Admin-only; backed by GET/PUT /api/v1/performance.
 */

import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Cpu, Info } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  fetchPerformanceSettings,
  updatePerformanceSettings,
  isApiError,
  type PerformanceSettingsUpdate,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SaveStatus, useSaveStatus } from './SaveStatus'
import { ReAuthDialog } from './ReAuthDialog'

// ── Skeleton ──────────────────────────────────────────────────────────────────

function Skeleton() {
  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-3 animate-pulse">
      <div className="h-4 w-48 rounded bg-[var(--color-border)]" />
      <div className="h-3 w-full rounded bg-[var(--color-border)]" />
      <div className="h-3 w-2/3 rounded bg-[var(--color-border)]" />
    </div>
  )
}

// ── Component ─────────────────────────────────────────────────────────────────

export function PerformanceSection(): React.ReactElement {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const { state: saveStatus, setState: setSaveStatus } = useSaveStatus()
  const [inputValue, setInputValue] = useState<string>('')
  const [dirty, setDirty] = useState(false)

  // The change waiting on a re-auth consent token, and whether the dialog is
  // open. PUT /api/v1/performance is re-auth gated (Spec-6 FR-12.2 / Spec-3
  // FR-6.6); the token is replayed via updatePerformanceSettings's header arg.
  const [pending, setPending] = useState<PerformanceSettingsUpdate | null>(null)
  const [reauthOpen, setReauthOpen] = useState(false)

  const { data, isLoading, error } = useQuery({
    queryKey: ['performance-settings'],
    queryFn: fetchPerformanceSettings,
    staleTime: 30_000,
  })

  // Sync input with fetched value on first load.
  useEffect(() => {
    if (data && !dirty) {
      const configured = data.max_parallel_agents ?? 0
      setInputValue(configured === 0 ? '' : String(configured))
    }
  }, [data, dirty])

  const mutation = useMutation({
    mutationFn: ({ body, token }: { body: PerformanceSettingsUpdate; token: string }) =>
      updatePerformanceSettings(body, token),
    onSuccess: () => {
      setSaveStatus('saved')
      setDirty(false)
      setPending(null)
      void queryClient.invalidateQueries({ queryKey: ['performance-settings'] })
    },
    onError: (err) => {
      setSaveStatus('error')
      setPending(null)
      const msg = isApiError(err) ? err.message : 'Failed to save performance settings.'
      addToast({ variant: 'error', message: msg })
    },
  })

  // handleSave validates then stages the change and opens the re-auth dialog.
  // The actual PUT fires from onReAuthConfirmed once the consent token is minted
  // — mirroring IntegrationsSection's gated-save flow.
  function handleSave() {
    const raw = inputValue.trim()
    const parsed = raw === '' ? 0 : parseInt(raw, 10)
    if (raw !== '' && (isNaN(parsed) || parsed < 2 || parsed > 16)) {
      addToast({ variant: 'error', message: 'max_parallel_agents must be between 2 and 16 (or leave blank for auto-detect).' })
      return
    }
    setPending({ max_parallel_agents: parsed })
    setReauthOpen(true)
  }

  function onReAuthConfirmed(token: string) {
    if (!pending) return
    setSaveStatus('saving')
    mutation.mutate({ body: pending, token })
  }

  if (isLoading) return <Skeleton />
  if (error) {
    return (
      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 text-sm text-[var(--color-warning)]">
        Could not load performance settings.
      </div>
    )
  }

  const effective = data?.effective_max_parallel_agents ?? '?'

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center gap-2">
        <Cpu size={18} className="text-[var(--color-secondary)]" />
        <h2 className="text-sm font-semibold text-[var(--color-secondary)]">Agent Concurrency</h2>
      </div>

      {/* Card */}
      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
        <div className="space-y-1">
          <p className="text-xs text-[var(--color-muted)] leading-relaxed">
            Controls how many tasks and subagents may run concurrently across all agents.
            The runtime clamps the value to <span className="font-mono">[2, min(NumCPU-2, RAM_GB/1.5)]</span> with a ceiling of 16.
            Leave blank to use the auto-detected default.
          </p>
          <div className="flex items-center gap-1 text-xs text-[var(--color-muted)]">
            <Info size={12} />
            <span>Effective value in use: <span className="font-mono font-medium text-[var(--color-secondary)]">{effective}</span></span>
          </div>
        </div>

        <div className="flex items-center gap-3">
          <label className="text-xs font-medium text-[var(--color-secondary)] w-44 shrink-0">
            Max parallel agents
          </label>
          <Input
            type="number"
            min={2}
            max={16}
            placeholder="auto"
            value={inputValue}
            onChange={(e) => {
              setInputValue(e.target.value)
              setDirty(true)
              setSaveStatus('idle')
            }}
            className="w-24 h-7 text-sm"
            aria-label="Max parallel agents"
          />
          <Button
            size="sm"
            onClick={handleSave}
            disabled={mutation.isPending || reauthOpen || !dirty}
            className="h-7 px-3 text-xs"
          >
            Save
          </Button>
          <SaveStatus state={saveStatus} />
        </div>
      </div>

      <ReAuthDialog
        open={reauthOpen}
        onOpenChange={(o) => {
          setReauthOpen(o)
          if (!o) {
            setPending(null)
            if (saveStatus === 'saving') setSaveStatus('idle')
          }
        }}
        title="Confirm to change concurrency"
        description="Re-type your password to change the max parallel agents setting."
        onConfirmed={onReAuthConfirmed}
      />
    </div>
  )
}
