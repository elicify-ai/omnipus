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

// The skeleton MUST carry text. Its bars are decorative divs with no text
// content, so a purely-visual skeleton makes the whole panel's innerText the
// empty string — indistinguishable, to a reader or a screen reader, from
// "there is nothing to configure here". That is exactly how this tab read
// during the retry window of a failing load (GET /api/v1/performance is
// retried three times with 1s/2s/4s backoff, so the panel sat textless for
// ~7 seconds before any error surfaced).
function Skeleton() {
  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="performance-loading"
      className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-3"
    >
      <p className="text-xs text-[var(--color-muted)]">Loading performance settings…</p>
      <div className="space-y-3 animate-pulse" aria-hidden="true">
        <div className="h-4 w-48 rounded bg-[var(--color-border)]" />
        <div className="h-3 w-full rounded bg-[var(--color-border)]" />
        <div className="h-3 w-2/3 rounded bg-[var(--color-border)]" />
      </div>
    </div>
  )
}

// ── Component ─────────────────────────────────────────────────────────────────

// Autosave debounce: wait 600 ms of inactivity before opening the reauth dialog.
const AUTOSAVE_DEBOUNCE_MS = 600

// Validation message shared by every path that rejects an invalid
// max_parallel_agents input (triggerSave, the debounced autosave settle, and
// the tools_on_demand toggle guard). Bounds mirror the backend contract
// (contracts/components/schemas/PerformanceSettingsUpdate.yaml): minimum 0,
// no ceiling — 0 (or a blank field) clears the explicit cap, any
// positive integer is honored exactly as configured.
const INVALID_MAX_PARALLEL_MESSAGE =
  'max_parallel_agents must be zero or a positive whole number (0 or blank means no explicit cap — concurrency is then bounded by available memory).'

// PHYSICAL_THREAD_CEILING mirrors pkg/config/config.go's
// physicalConcurrencySafetyCeiling — the point above which the backend
// itself logs a WARN (Go's runtime hard-aborts the process past 10,000 OS
// threads; this leaves a 5x margin). It is NOT enforced as a limit — the
// backend honors any explicit value in full — so the frontend must only ever
// caution here, never claim the value will be lowered.
//
// It is ALSO the value effective_max_parallel_agents carries when nothing is
// configured, which is precisely why that case must never be rendered as a
// recommendation: the backstop answers "what would abort the Go runtime",
// not "what can this machine run". See max_parallel_agents_configured.
const PHYSICAL_THREAD_CEILING = 2000

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

  const { data, isLoading, error, refetch, isFetching } = useQuery({
    queryKey: ['performance-settings'],
    queryFn: fetchPerformanceSettings,
    staleTime: 30_000,
  })

  // Sync inputs with fetched values on first load.
  useEffect(() => {
    if (data && !dirty) {
      // max_parallel_agents is NOT the configured value when nothing is
      // configured. The backend substitutes the resolved effective value
      // there, because 0 is an internal sentinel the schema forbids on the
      // wire (minimum: 1). So on an unconfigured install this field carries
      // the physical OS-thread backstop, and prefilling the input with it
      // would let an operator who saves without touching anything silently
      // turn a memory-bounded install into one with an explicit cap of 2000.
      // max_parallel_agents_configured is what distinguishes the two.
      const configured =
        data.max_parallel_agents_configured === false ? 0 : (data.max_parallel_agents ?? 0)
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
    // Backend bound (PerformanceSettingsUpdate.yaml): minimum 0, no ceiling.
    // 0 (or blank, which we treat as 0 above) means "no explicit cap";
    // any positive integer is honored exactly as configured — there is no
    // upper limit to validate against here.
    if (raw !== '' && (isNaN(parsed) || parsed < 0)) return null
    return { max_parallel_agents: parsed, tools_on_demand: onDemand }
  }, [])

  // triggerSave validates the current input and opens the ReAuthDialog.
  // The actual PUT fires from onReAuthConfirmed once the consent token is minted.
  const triggerSave = useCallback(() => {
    const body = buildBody(inputValue, toolsOnDemand)
    if (!body) {
      addToast({ variant: 'error', message: INVALID_MAX_PARALLEL_MESSAGE })
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
        addToast({ variant: 'error', message: INVALID_MAX_PARALLEL_MESSAGE })
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
      addToast({ variant: 'error', message: INVALID_MAX_PARALLEL_MESSAGE })
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
    // State the underlying cause, and give the reader a way out. This mirrors
    // the sibling tabs on this same screen — Security's SkillTrustSection
    // renders "Failed to load skill trust settings: <cause>", and Gateway
    // banners the reason — rather than a bare sentence that tells an operator
    // nothing about whether to wait, re-authenticate, or check the backend.
    return (
      <div
        role="alert"
        data-testid="performance-load-error"
        className="rounded-lg border border-[var(--color-error)]/40 bg-[var(--color-error)]/10 p-4 space-y-2"
      >
        <div className="flex items-start gap-2 text-sm text-[var(--color-error)]">
          <Warning size={16} className="mt-0.5 shrink-0" />
          <span>
            Failed to load performance settings: {getErrorMessage(error, 'Unknown error')}
          </span>
        </div>
        <button
          type="button"
          tabIndex={0}
          data-testid="performance-retry-btn"
          onClick={() => void refetch()}
          disabled={isFetching}
          className="text-xs font-medium underline text-[var(--color-secondary)] disabled:opacity-50"
        >
          {isFetching ? 'Retrying…' : 'Retry'}
        </button>
      </div>
    )
  }

  const effective = data?.effective_max_parallel_agents ?? '?'

  // Is anything actually configured?
  //
  // This is the whole point of the max_parallel_agents_configured field.
  // There is no longer a computed default: when nothing is configured, the
  // backend bounds concurrency by LIVE available memory at the moment each
  // agent turn is admitted, and effective_max_parallel_agents carries a
  // PHYSICAL OS-thread-safety backstop (2000) rather than a capacity
  // estimate. Rendering that integer under the words "Recommended" told
  // every operator on an unconfigured install that the system recommends
  // 2000 parallel agents — a number nothing in the process was claiming.
  //
  // Note the `!== false` rather than a truthy check: the field is optional on
  // the wire, and an older backend that omits it should keep the previous
  // (integer) rendering rather than silently switching every install to the
  // automatic text.
  const isConfigured = data?.max_parallel_agents_configured !== false
  const recommended =
    isConfigured && typeof effective === 'number' ? effective : undefined

  // High-value caution: when the typed value exceeds the physical OS-thread
  // safety ceiling the backend itself warns about (physicalConcurrencySafetyCeiling,
  // pkg/config/config.go), surface an inline caution. This does NOT mean the
  // value will be lowered — explicit values have no ceiling and are always
  // honored exactly as configured — it only flags the real thread-exhaustion
  // risk the backend's own WARN log calls out at the same threshold.
  const inputValueNum = inputValue.trim() === '' ? null : parseInt(inputValue, 10)
  const exceedsPhysicalCeiling =
    inputValueNum !== null &&
    !isNaN(inputValueNum) &&
    inputValueNum > PHYSICAL_THREAD_CEILING

  const recommendationText =
    typeof recommended === 'number'
      ? `Currently in use: ${recommended} parallel agents`
      : 'automatic \u2014 bounded by available memory'

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

      {/* Live concurrency card — shown above the input */}
      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] p-3 flex items-start gap-2">
        <Info size={14} className="text-[var(--color-accent)] mt-0.5 shrink-0" />
        <div className="flex-1 min-w-0">
          <p className="text-xs text-[var(--color-secondary)] leading-relaxed">
            {recommendationText}
          </p>
          <p className="text-[11px] text-[var(--color-muted)] mt-0.5">
            {isConfigured
              ? 'An explicit value has no ceiling — it is always honored exactly as set. Agent turns are still admitted only while the host has memory to spare.'
              : 'Nothing is configured, so concurrency is bounded by this host\u2019s available memory at the moment each agent turn starts. Set a value below to cap it explicitly instead.'}
          </p>
        </div>
      </div>

      {/* Concurrency card */}
      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
        <div className="space-y-1">
          <p className="text-xs text-[var(--color-muted)] leading-relaxed">
            Controls how many tasks and subagents may run concurrently across all agents.
            Leave blank for no explicit cap — concurrency is then bounded by available memory. Changes apply after re-authentication.
          </p>
          <div className="flex items-center gap-1 text-xs text-[var(--color-muted)]">
            <Info size={12} />
            <span>
              Effective value in use:{' '}
              <span className="font-mono font-medium text-[var(--color-secondary)]">
                {isConfigured ? effective : 'automatic'}
              </span>
            </span>
          </div>
        </div>

        <div className="flex items-center gap-3">
          <label className="text-xs font-medium text-[var(--color-secondary)] w-44 shrink-0">
            Max parallel agents
          </label>
          <Input
            type="number"
            min={0}
            placeholder="auto"
            value={inputValue}
            onChange={(e) => handleInputChange(e.target.value)}
            className="w-24 h-7 text-sm"
            aria-label="Max parallel agents"
            data-testid="performance-max-agents-input"
          />
        </div>

        {/* High-value caution — yellow inline notice. Unlike the old
            over-limit warning this replaces, it does NOT claim the value
            will be lowered: explicit values have no ceiling and are always
            honored exactly as configured. It only surfaces the same
            thread-exhaustion risk the backend's own WARN log calls out
            above physicalConcurrencySafetyCeiling. */}
        {exceedsPhysicalCeiling && (
          <div
            data-testid="performance-high-value-warning"
            className="flex items-start gap-2 p-2.5 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 text-xs text-[var(--color-warning)]"
          >
            <Warning size={14} className="mt-0.5 shrink-0" />
            <span>
              {inputValueNum} will be honored exactly as set — there is no ceiling — but values
              this high risk Go runtime thread exhaustion, which aborts the process. The
              backend logs a warning above {PHYSICAL_THREAD_CEILING}; consider whether you
              really need this many concurrent agents.
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
        <button tabIndex={0}
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
