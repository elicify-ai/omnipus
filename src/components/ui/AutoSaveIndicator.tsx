import { Check, CircleNotch, Warning } from '@phosphor-icons/react'
import type { AutoSaveStatus } from '@/hooks/useAutoSave'

interface AutoSaveIndicatorProps {
  status: AutoSaveStatus
  error?: string
  className?: string
  /** Timestamp of the last successful save, surfaced when status === 'saved'. */
  lastSavedAt?: Date | null
}

function formatSavedAt(date: Date): string {
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffSec = Math.floor(diffMs / 1000)
  if (diffSec < 5) return 'Saved just now'
  if (diffSec < 60) return `Saved ${diffSec}s ago`
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `Saved ${diffMin}m ago`
  return `Saved at ${date.toLocaleTimeString()}`
}

/**
 * Subtle auto-save status indicator.
 * idle → hidden, saving → spinner, saved → checkmark + last saved time (fades), error → red warning.
 */
export function AutoSaveIndicator({ status, error, className = '', lastSavedAt }: AutoSaveIndicatorProps) {
  if (status === 'idle') return null

  return (
    <span
      className={`inline-flex items-center gap-1 text-[10px] transition-opacity duration-300 ${
        status === 'saved' ? 'opacity-60' : 'opacity-100'
      } ${className}`}
    >
      {status === 'saving' && (
        <>
          <CircleNotch size={11} className="animate-spin text-[var(--color-muted)]" />
          <span className="text-[var(--color-muted)]">Saving...</span>
        </>
      )}
      {status === 'saved' && (
        <>
          <Check size={11} weight="bold" className="text-emerald-400" />
          <span className="text-emerald-400">
            {lastSavedAt ? formatSavedAt(lastSavedAt) : 'Saved'}
          </span>
        </>
      )}
      {status === 'error' && (
        <>
          <Warning size={11} weight="bold" className="text-[var(--color-error)]" />
          <span className="text-[var(--color-error)]">{error || 'Save failed'}</span>
        </>
      )}
    </span>
  )
}
