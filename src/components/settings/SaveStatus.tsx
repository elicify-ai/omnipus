import { useState, useEffect } from 'react'
import { CheckCircle, CircleNotch, Warning } from '@phosphor-icons/react'

// ── Types ─────────────────────────────────────────────────────────────────────

export type SaveState = 'idle' | 'saving' | 'saved' | 'error'

// ── Component ─────────────────────────────────────────────────────────────────

export function SaveStatus({
  state,
  errorMessage,
}: {
  state: SaveState
  errorMessage?: string
}) {
  if (state === 'idle') return null

  if (state === 'saving') {
    return (
      <span
        className="inline-flex items-center gap-1 text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
        aria-live="polite"
      >
        <CircleNotch size={12} className="animate-spin" />
        Saving…
      </span>
    )
  }

  if (state === 'saved') {
    return (
      <span
        className="inline-flex items-center gap-1 text-[length:var(--type-utility-xs-size)]"
        style={{ color: 'var(--color-success)' }}
        aria-live="polite"
      >
        <CheckCircle size={12} weight="fill" />
        Saved
      </span>
    )
  }

  return (
    <span
      className="inline-flex items-center gap-1 text-[length:var(--type-utility-xs-size)]"
      style={{ color: 'var(--color-error)' }}
      role="alert"
      title={errorMessage}
    >
      <Warning size={12} weight="fill" />
      Save failed
    </span>
  )
}

// ── Hook ──────────────────────────────────────────────────────────────────────

export function useSaveStatus() {
  const [state, setState] = useState<SaveState>('idle')
  const [errorMessage, setErrorMessage] = useState<string | undefined>()

  useEffect(() => {
    if (state !== 'saved') return
    const t = setTimeout(() => setState('idle'), 2000)
    return () => clearTimeout(t)
  }, [state])

  return { state, setState, errorMessage, setErrorMessage }
}
