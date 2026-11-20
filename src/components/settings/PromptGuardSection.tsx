import { useState, useEffect } from 'react'
import { ReadErrorCallout } from './ReadErrorCallout'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Shield } from '@phosphor-icons/react'
import { fetchPromptGuardLevel, updatePromptGuardLevel, getErrorMessage } from '@/lib/api'
import type { PromptInjectionLevel } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { Card } from '@/components/ui/card'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { SaveStatus, useSaveStatus } from './SaveStatus'
import type { FieldMessage } from './useSaveBar'
import { cn } from '@/lib/utils'

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
    <Card className="p-[var(--space-3)] space-y-[var(--space-2-5)] animate-pulse">
      <div className="h-4 w-40 rounded bg-[var(--color-border)]" />
      <div className="h-3 w-full rounded bg-[var(--color-border)]" />
      <div className="h-3 w-full rounded bg-[var(--color-border)]" />
    </Card>
  )
}

// ── Component ─────────────────────────────────────────────────────────────────

export interface PromptGuardDraft { // not-wire-format: React props from Protection's Save bar
  /** The level shown: the draft, or the saved level. */
  value: PromptInjectionLevel
  onChange: (next: PromptInjectionLevel) => void
  /** The Save bar's line for this value (an error or "Saved, but not in use yet"). */
  message?: FieldMessage
  /** data-settings-field id, used to move focus to it. */
  fieldId: string
}

/**
 * With `draft` (Settings → Protection, Settings redesign #1519 FR-010), the
 * level is a draft on Protection's Save bar and nothing is sent until Save
 * changes; the Save bar reports the outcome and any restart. Without it (the
 * legacy view), it saves at once as before.
 */
export function PromptGuardSection({ draft }: { draft?: PromptGuardDraft } = {}): React.ReactElement {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const { data, isLoading, isError, error, refetch, isFetching } = useQuery({
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
    if (draft) {
      draft.onChange(level)
      return
    }
    setSelected(level)
    save(level)
  }
  const shown = draft ? draft.value : selected

  if (isLoading) return <Skeleton />

  if (isError) {
    return (
      <ReadErrorCallout
        title="Failed to load prompt injection defense"
        error={error}
        onRetry={() => void refetch()}
        retrying={isFetching}
        data-testid="prompt-guard-load-error"
      />
    )
  }

  return (
    <section className="space-y-[var(--space-2-5)]">
      <div className="flex items-center justify-between">
        <h3 className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)] flex items-center gap-[var(--space-1)]">
          <Shield size={14} className="text-[var(--color-muted)]" />
          Prompt Injection Defense
          {!draft && restartRequired && (
            <span className="ml-[var(--space-2)] text-[length:var(--type-caption-size)] uppercase tracking-wider text-[var(--color-warning)] border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 rounded px-[var(--space-1)] py-[var(--space-0-5)]">
              Restart required
            </span>
          )}
        </h3>
        {!draft && <SaveStatus state={saveState} errorMessage={errorMessage} />}
      </div>

      <Card className="p-[var(--space-3)] space-y-[var(--space-3)]">
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">
          Controls how untrusted tool output is sanitised before passing to the agent.
        </p>

        <RadioGroup
          className="space-y-[var(--space-2)]"
          orientation="vertical"
          aria-label="Prompt injection defense level"
          value={shown}
          onValueChange={(value) => handleChange(value as PromptInjectionLevel)}
          data-settings-field={draft?.fieldId}
        >
          {LEVELS.map((lvl) => {
            const isActive = shown === lvl.value
            return (
              <RadioGroupItem
                key={lvl.value}
                value={lvl.value}
                className={[
                  'flex-col items-start gap-0 rounded-md border p-[var(--space-2-5)] transition-colors',
                  isActive
                    ? 'border-[var(--color-accent)]/60 bg-[var(--color-accent)]/8 hover:bg-[var(--color-accent)]/8'
                    : 'border-[var(--color-border)] bg-[var(--color-surface-2)] hover:border-[var(--color-border)]/80 hover:bg-[var(--color-surface-2)]',
                ].join(' ')}
              >
                <div className="flex items-center gap-[var(--space-2)]">
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
                      'text-[length:var(--type-body-compact-size)] font-medium',
                      isActive ? 'text-[var(--color-secondary)]' : 'text-[var(--color-muted)]',
                    ].join(' ')}
                  >
                    {lvl.label}
                  </span>
                </div>
                <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-1)] ml-[var(--space-3)] leading-relaxed">
                  {lvl.subtitle}
                </p>
              </RadioGroupItem>
            )
          })}
        </RadioGroup>
        {draft?.message && (
          <p
            role={draft.message.tone === 'error' ? 'alert' : undefined}
            className={cn(
              'text-[length:var(--type-body-compact-size)]',
              draft.message.tone === 'error' ? 'text-[var(--color-error)]' : 'text-[var(--color-warning)]',
            )}
          >
            {draft.message.text}
          </p>
        )}
      </Card>
    </section>
  )
}
