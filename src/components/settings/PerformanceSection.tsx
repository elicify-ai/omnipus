/**
 * PerformanceSection — Settings → Performance tab.
 *
 * Spec-3 max-parallel fan-out gate: lets an admin configure
 * max_parallel_agents (the global dispatch semaphore capacity) and
 * tools_on_demand (the tool-loading mode).
 * Admin-only; backed by GET/PUT /api/v1/performance.
 *
 * Autosave: changes are applied automatically after a short debounce.
 * Because PUT /api/v1/performance is re-auth gated (Spec-6 FR-12.2 /
 * Spec-3 FR-6.6), the ReAuthDialog is opened automatically once the
 * debounced value settles on a valid input — the Save button is gone.
 *
 * Both max_parallel_agents and tools_on_demand are sent together on every
 * PUT so neither field silently reverts when only one is changed.
 */

import { useState, useEffect, useRef, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Cpu, Info, Warning } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  fetchPerformanceSettings,
  updatePerformanceSettings,
  getErrorMessage,
  type PerformanceSettingsUpdate,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import type { AutoSaveStatus } from '@/hooks/useAutoSave'
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

// Autosave debounce: wait 600 ms of inactivity before opening the reauth dialog.
const AUTOSAVE_DEBOUNCE_MS = 600

export function PerformanceSection(): React.ReactElement {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const [saveStatus, setSaveStatus] = useState<AutoSaveStatus>('idle')
  const [inputValue, setInputValue] = useState<string>('')
  // toolsOnDemand mirrors the tools_on_demand field. true = load on demand (default).
  const [toolsOnDemand, setToolsOnDemand] = useState<boolean>(true)
  // dirty tracks whether the user has changed any field since the last save.
  const [dirty, setDirty] = useState(false)

  // The change waiting on a re-auth consent token, and whether the dialog is
  // open. PUT /api/v1/performance is re-auth gated (Spec-6 FR-12.2 / Spec-3
  // FR-6.6); the token is replayed via updatePerformanceSettings's header arg.
  const [pending, setPending] = useState<PerformanceSettingsUpdate | null>(null)
  const [reauthOpen, setReauthOpen] = useState(false)

  // Debounce timer for autosave.
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const { data, isLoading, error } = useQuery({
    queryKey: ['performance-settings'],
    queryFn: fetchPerformanceSettings,
    staleTime: 30_000,
  })

  // Sync inputs with fetched values on first load.
  useEffect(() => {
    if (data && !dirty) {
      const configured = data.max_parallel_agents ?? 0
      setInputValue(configured === 0 ? '' : String(configured))
      // tools_on_demand defaults to true when absent from the response.
      setToolsOnDemand(data.tools_on_demand ?? true)
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
      // Reset to 'idle' after showing 'saved' briefly.
      setTimeout(() => setSaveStatus('idle'), 2000)
    },
    onError: (err) => {
      setSaveStatus('error')
      setPending(null)
      const msg = getErrorMessage(err, 'Failed to save performance settings.')
      addToast({ variant: 'error', message: msg })
    },
  })

  // buildBody constructs the full update payload using the latest local state.
  // Both fields are always sent together so neither reverts when only one changes.
  const buildBody = useCallback((
    rawInput: string,
    onDemand: boolean,
  ): PerformanceSettingsUpdate | null => {
    const raw = rawInput.trim()
    const parsed = raw === '' ? 0 : parseInt(raw, 10)
    if (raw !== '' && (isNaN(parsed) || parsed < 2 || parsed > 16)) return null
    return { max_parallel_agents: parsed, tools_on_demand: onDemand }
  }, [])

  // triggerSave validates the current input and opens the ReAuthDialog.
  // The actual PUT fires from onReAuthConfirmed once the consent token is minted.
  const triggerSave = useCallback(() => {
    const body = buildBody(inputValue, toolsOnDemand)
    if (!body) {
      addToast({ variant: 'error', message: 'max_parallel_agents must be between 2 and 16 (or leave blank for auto-detect).' })
      return
    }
    setPending(body)
    setReauthOpen(true)
  }, [inputValue, toolsOnDemand, buildBody, addToast])

  // Autosave: debounce on input change then open the reauth dialog.
  function handleInputChange(value: string) {
    setInputValue(value)
    setDirty(true)
    setSaveStatus('idle')
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      const body = buildBody(value, toolsOnDemand)
      if (body) {
        setSaveStatus('saving')
        setPending(body)
        setReauthOpen(true)
      } else {
        // max_parallel_agents settled out of range — this path never goes
        // through triggerSave, so without this branch the debounce would
        // silently no-op: no toast, no indication the value wasn't saved.
        // Mirror the same guidance triggerSave and the toggle path show.
        setSaveStatus('idle')
        addToast({ variant: 'error', message: 'max_parallel_agents must be between 2 and 16 (or leave blank for auto-detect).' })
      }
    }, AUTOSAVE_DEBOUNCE_MS)
  }

  // handleToolsOnDemandChange fires immediately (no debounce) — a toggle is an
  // unambiguous user action that doesn't need a settling delay.
  function handleToolsOnDemandChange(checked: boolean) {
    const previousToolsOnDemand = toolsOnDemand
    setToolsOnDemand(checked)
    setDirty(true)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    const body = buildBody(inputValue, checked)
    if (body) {
      setSaveStatus('saving')
      setPending(body)
      setReauthOpen(true)
    } else {
      // max_parallel_agents is out of range — the toggle can't proceed until
      // the numeric field is fixed. Revert the switch (it was optimistically
      // flipped above but nothing will be saved), clear any stale 'saving'
      // spinner so it doesn't stick forever, and tell the user why.
      setToolsOnDemand(previousToolsOnDemand)
      setSaveStatus('idle')
      addToast({ variant: 'error', message: 'max_parallel_agents must be between 2 and 16 (or leave blank for auto-detect).' })
    }
  }

  // Cleanup debounce timer on unmount.
  useEffect(() => {
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [])

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

  // Live system recommendation. The browser exposes logical core count via
  // navigator.hardwareConcurrency; the auto-detect formula clamps the
  // configured value to [2, min(NumCPU-2, RAM_GB/1.5)] with a ceiling of 16.
  // The effective value returned by the API already applies this clamp, so we
  // surface it as the recommended concurrency. RAM is not exposed to the
  // browser for privacy reasons, so the recommendation is CPU-bounded here.
  const cpuCores = typeof navigator !== 'undefined' ? navigator.hardwareConcurrency : undefined
  const cpuUpperBound = cpuCores ? Math.min(Math.max(cpuCores - 2, 2), 16) : undefined
  const recommended = typeof effective === 'number' ? effective : cpuUpperBound

  // Over-limit warning: when the user has typed a value above the recommended
  // ceiling, surface a yellow inline warning so they understand the runtime
  // will clamp it down.
  const inputValueNum = inputValue.trim() === '' ? null : parseInt(inputValue, 10)
  const overLimit =
    inputValueNum !== null &&
    !isNaN(inputValueNum) &&
    typeof recommended === 'number' &&
    inputValueNum > recommended

  const coresPart = cpuCores ? `${cpuCores} cores` : 'cores unavailable'
  const recPart = typeof recommended === 'number' ? `${recommended} parallel agents` : 'auto-detect'
  const recommendationText = `Your system: ${coresPart} → Recommended: ${recPart}`

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Cpu size={18} className="text-[var(--color-secondary)]" />
          <h2 className="text-sm font-semibold text-[var(--color-secondary)]">Agent Concurrency</h2>
        </div>
        <AutoSaveIndicator status={saveStatus} />
      </div>

      {/* Live recommendation card — shown above the input */}
      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] p-3 flex items-start gap-2">
        <Info size={14} className="text-[var(--color-accent)] mt-0.5 shrink-0" />
        <div className="flex-1 min-w-0">
          <p className="text-xs text-[var(--color-secondary)] leading-relaxed">
            {recommendationText}
          </p>
          <p className="text-[11px] text-[var(--color-muted)] mt-0.5">
            Auto-detected from CPU/RAM heuristics. The runtime clamps to <span className="font-mono">[2, min(NumCPU-2, RAM_GB/1.5)]</span> with a ceiling of 16.
          </p>
        </div>
      </div>

      {/* Concurrency card */}
      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
        <div className="space-y-1">
          <p className="text-xs text-[var(--color-muted)] leading-relaxed">
            Controls how many tasks and subagents may run concurrently across all agents.
            Leave blank to use the auto-detected default. Changes apply after re-authentication.
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
            onChange={(e) => handleInputChange(e.target.value)}
            className="w-24 h-7 text-sm"
            aria-label="Max parallel agents"
            data-testid="performance-max-agents-input"
          />
        </div>

        {/* Over-limit warning — yellow inline notice */}
        {overLimit && (
          <div
            data-testid="performance-over-limit-warning"
            className="flex items-start gap-2 p-2.5 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 text-xs text-[var(--color-warning)]"
          >
            <Warning size={14} className="mt-0.5 shrink-0" />
            <span>
              {inputValueNum} exceeds the recommended {recommended}. The runtime will clamp
              the effective value to {recommended} — consider lowering the setting.
            </span>
          </div>
        )}
      </div>

      {/* Tool loading card */}
      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-3">
        {/* Section heading */}
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-semibold text-[var(--color-secondary)]">Tool loading</h3>
        </div>

        {/* Toggle row */}
        <div className="flex items-start justify-between gap-4">
          <div className="flex-1 min-w-0">
            {toolsOnDemand ? (
              <>
                <p className="text-sm text-[var(--color-secondary)]">Load tools on demand</p>
                <p className="text-xs text-[var(--color-muted)] mt-0.5">
                  Smaller messages, lower token use. Recommended.
                </p>
              </>
            ) : (
              <>
                <p className="text-sm text-[var(--color-secondary)]">Keep all tools loaded</p>
                <p className="text-xs text-[var(--color-muted)] mt-0.5">
                  Every tool is always available — no loading step, but larger messages.
                </p>
              </>
            )}
          </div>
          <Switch
            checked={toolsOnDemand}
            onCheckedChange={handleToolsOnDemandChange}
            disabled={mutation.isPending}
            aria-label="Tool loading"
            data-testid="performance-tools-on-demand-switch"
          />
        </div>

        {/* Helper text */}
        <p className="text-[11px] text-[var(--color-muted)] leading-relaxed">
          Applies to all agents. Takes effect on the next message — no restart required.
          Changes apply after re-authentication.
        </p>
      </div>

      <ReAuthDialog
        open={reauthOpen}
        onOpenChange={(o) => {
          setReauthOpen(o)
          if (!o) {
            setPending(null)
            if (saveStatus === 'saving') setSaveStatus('idle')
            // Cancelling re-auth means the pending change (toggle or typed
            // value) was never persisted. Clear dirty so the sync effect
            // above (`data && !dirty`) re-applies the last-known-good server
            // values — otherwise the switch/input would keep showing the
            // unsaved edit indefinitely, until the user happened to change
            // it again.
            setDirty(false)
          }
        }}
        title="Confirm to change performance settings"
        description="Re-type your password to save the performance settings."
        onConfirmed={onReAuthConfirmed}
      />

      {/* Escape hatch: manual trigger exposed for keyboard users / edge cases */}
      {dirty && !reauthOpen && (
        <button
          type="button"
          data-testid="performance-save-btn"
          onClick={triggerSave}
          className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:p-2 focus:bg-[var(--color-surface-1)] focus:rounded text-xs text-[var(--color-secondary)]"
        >
          Save changes
        </button>
      )}
    </div>
  )
}
