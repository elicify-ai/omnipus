/**
 * SkillTrustSection — Block / Warn-unverified / Allow-all tri-state radio.
 *
 * Restart-required wiring (MEDIUM fix):
 *   The local `restartRequired` useState was lost on unmount/collapse and
 *   was also dead (the backend always returns requires_restart=false for skill
 *   trust). Replaced with `queryClient.invalidateQueries(PENDING_RESTART_QUERY_KEY)`
 *   on every successful save so the shared RestartBanner reflects any future
 *   backend change without component-local state. The banner is data-driven from
 *   the pending-restart poll; SkillTrustSection no longer tracks it locally.
 */

import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Package, Warning } from '@phosphor-icons/react'
import { fetchSkillTrust, updateSkillTrust, getErrorMessage } from '@/lib/api'
import type { SkillTrustLevel } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SaveStatus, useSaveStatus } from './SaveStatus'
import { PENDING_RESTART_QUERY_KEY } from '@/hooks/restart'

// ── Level metadata ────────────────────────────────────────────────────────────

const LEVELS: { value: SkillTrustLevel; label: string; subtitle: string }[] = [
  {
    value: 'block_unverified',
    label: 'Block unverified',
    subtitle: 'Block skills without a verifiable hash',
  },
  {
    value: 'warn_unverified',
    label: 'Warn unverified',
    subtitle: 'Warn but allow (default)',
  },
  {
    value: 'allow_all',
    label: 'Allow all',
    subtitle:
      'Accept any skill — disables hash verification. Only use with a trusted skills registry.',
  },
]

// ── Skeleton ──────────────────────────────────────────────────────────────────

function Skeleton() {
  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-3 animate-pulse">
      <div className="h-4 w-40 rounded bg-[var(--color-border)]" />
      <div className="h-3 w-full rounded bg-[var(--color-border)]" />
      <div className="h-3 w-full rounded bg-[var(--color-border)]" />
    </div>
  )
}

// ── Component ─────────────────────────────────────────────────────────────────

export function SkillTrustSection(): React.ReactElement {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['skill-trust'],
    queryFn: fetchSkillTrust,
  })

  const [selected, setSelected] = useState<SkillTrustLevel>('warn_unverified')

  const { state: saveState, setState: setSaveState, errorMessage, setErrorMessage } = useSaveStatus()

  useEffect(() => {
    if (!data) return
    setSelected(data.level)
  }, [data])

  const { mutate: save } = useMutation({
    mutationFn: (level: SkillTrustLevel) => updateSkillTrust(level),
    onMutate: () => setSaveState('saving'),
    onSuccess: (resp) => {
      setSaveState('saved')
      queryClient.setQueryData(['skill-trust'], { level: resp.applied_level })
      // Wire any requires_restart signal into the shared pending-restart store
      // so RestartBanner picks it up cross-cutting, rather than losing it when
      // this component unmounts or the Advanced disclosure collapses.
      if (resp.requires_restart) {
        void queryClient.invalidateQueries({ queryKey: [...PENDING_RESTART_QUERY_KEY] })
      }
    },
    onError: (err: Error) => {
      setSaveState('error')
      const msg = getErrorMessage(err, 'Save failed')
      setErrorMessage(msg)
      addToast({ message: msg, variant: 'error' })
    },
  })

  function handleChange(level: SkillTrustLevel) {
    setSelected(level)
    save(level)
  }

  if (isLoading) return <Skeleton />

  if (isError) {
    return (
      <p className="text-sm" style={{ color: 'var(--color-error)' }}>
        Failed to load skill trust settings: {error instanceof Error ? error.message : 'Unknown error'}
      </p>
    )
  }

  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-[var(--color-secondary)] flex items-center gap-1.5">
          <Package size={14} className="text-[var(--color-muted)]" />
          Skill Trust
        </h3>
        <SaveStatus state={saveState} errorMessage={errorMessage} />
      </div>

      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
        <p className="text-xs text-[var(--color-muted)] leading-relaxed">
          Controls how unverified community skills are handled during installation and execution.
        </p>

        <div className="space-y-2" role="radiogroup" aria-label="Skill trust level">
          {LEVELS.map((lvl) => {
            const isActive = selected === lvl.value
            return (
              <button
                key={lvl.value}
                type="button"
                role="radio"
                aria-checked={isActive}
                onClick={() => {
                  if (selected !== lvl.value) handleChange(lvl.value)
                }}
                className={[
                  'w-full text-left rounded-md border p-3 transition-colors disabled:opacity-60 disabled:cursor-not-allowed',
                  isActive
                    ? 'border-[var(--color-accent)]/60 bg-[var(--color-accent)]/8'
                    : 'border-[var(--color-border)] bg-[var(--color-surface-2)] hover:border-[var(--color-border-hover)]',
                ].join(' ')}
              >
                <div className="flex items-center gap-2">
                  <span
                    className={[
                      'flex-shrink-0 inline-block w-3.5 h-3.5 rounded-full border-2 transition-colors',
                      isActive
                        ? 'border-[var(--color-accent)] bg-[var(--color-accent)]'
                        : 'border-[var(--color-border)]',
                    ].join(' ')}
                    aria-hidden="true"
                  />
                  <span
                    className={[
                      'text-sm font-medium',
                      isActive ? 'text-[var(--color-secondary)]' : 'text-[var(--color-muted)]',
                    ].join(' ')}
                  >
                    {lvl.label}
                  </span>
                </div>
                <p className="text-xs text-[var(--color-muted)] mt-1 ml-5 leading-relaxed">
                  {lvl.subtitle}
                </p>
              </button>
            )
          })}
        </div>

        {/* Warning panel when allow_all is selected */}
        {selected === 'allow_all' && (
          <div
            role="alert"
            className="flex items-start gap-2 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/8 p-3"
          >
            <Warning size={14} weight="fill" className="mt-0.5 shrink-0" style={{ color: 'var(--color-warning)' }} />
            <p className="text-xs leading-relaxed" style={{ color: 'var(--color-warning)' }}>
              This disables one of your key supply-chain protections. Prefer{' '}
              <span className="font-mono">warn_unverified</span> for normal operation.
            </p>
          </div>
        )}
      </div>
    </section>
  )
}
