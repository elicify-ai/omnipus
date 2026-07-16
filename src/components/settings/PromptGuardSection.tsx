import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Shield } from '@phosphor-icons/react'
import { fetchPromptGuardLevel, updatePromptGuardLevel, getErrorMessage } from '@/lib/api'
import type { PromptInjectionLevel } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SaveStatus, useSaveStatus } from './SaveStatus'

// ── Level metadata ────────────────────────────────────────────────────────────

const LEVELS: { value: PromptInjectionLevel; label: string; subtitle: string }[] = [
  {
    value: 'low',
    label: 'Low',
    subtitle:
      'Minimal sanitization. Tool output reaches the model with only basic cleanup.',
  },
  {
    value: 'medium',
    label: 'Medium',
    subtitle:
      'Balanced sanitization. Strips common prompt-injection patterns from tool output. (Default.)',
  },
  {
    value: 'high',
    label: 'High',
    subtitle:
      'Aggressive sanitization. Strips more patterns — may clip legitimate content.',
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

export function PromptGuardSection(): React.ReactElement {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['prompt-guard-k'],
    queryFn: fetchPromptGuardLevel,
  })

  const [selected, setSelected] = useState<PromptInjectionLevel>('medium')
  const [restartRequired, setRestartRequired] = useState(false)

  const { state: saveState, setState: setSaveState, errorMessage, setErrorMessage } = useSaveStatus()

  useEffect(() => {
    if (!data) return
    setSelected(data.level)
  }, [data])

  const { mutate: save } = useMutation({
    mutationFn: (level: PromptInjectionLevel) => updatePromptGuardLevel(level),
    onMutate: () => setSaveState('saving'),
    onSuccess: (resp) => {
      setSaveState('saved')
      if (resp.requires_restart) setRestartRequired(true)
      queryClient.setQueryData(['prompt-guard-k'], { level: resp.applied_level })
    },
    onError: (err: Error) => {
      setSaveState('error')
      const msg = getErrorMessage(err, 'Save failed')
      setErrorMessage(msg)
      addToast({ message: msg, variant: 'error' })
    },
  })

  function handleChange(level: PromptInjectionLevel) {
    setSelected(level)
    save(level)
  }

  if (isLoading) return <Skeleton />

  if (isError) {
    return (
      <p className="text-sm" style={{ color: 'var(--color-error)' }}>
        Failed to load prompt guard settings:{' '}
        {error instanceof Error ? error.message : 'Unknown error'}
      </p>
    )
  }

  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-[var(--color-secondary)] flex items-center gap-1.5">
          <Shield size={14} className="text-[var(--color-muted)]" />
          Prompt Injection Defense
          {restartRequired && (
            <span className="ml-2 text-[10px] uppercase tracking-wider text-[var(--color-warning)] border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 rounded px-1.5 py-0.5">
              Restart required
            </span>
          )}
        </h3>
        <SaveStatus state={saveState} errorMessage={errorMessage} />
      </div>

      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
        <p className="text-xs text-[var(--color-muted)] leading-relaxed">
          Controls how untrusted tool output is sanitised before passing to the agent.
        </p>

        <div className="space-y-2" role="radiogroup" aria-label="Prompt injection defense level">
          {LEVELS.map((lvl) => {
            const isActive = selected === lvl.value
            return (
              <button tabIndex={0}
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
      </div>
    </section>
  )
}
