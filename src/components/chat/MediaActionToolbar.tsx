// MediaActionToolbar — reusable row of icon-action buttons for chat media.
// Used both inline (hover on an image/diagram) and inside the lightbox.

import { useState, useRef, useEffect, type ReactNode } from 'react'
import { Check } from '@phosphor-icons/react'
import { useUiStore } from '@/store/ui'

// ── Types ──────────────────────────────────────────────────────────────────────

export interface MediaAction {
  /** Phosphor icon element (or any ReactNode) to display on the button. */
  icon: ReactNode
  /** Accessible label — used as aria-label and title. */
  label: string
  /** Handler; may return a Promise. */
  onClick: () => void | Promise<void>
  /**
   * If provided, the button temporarily shows a Check icon and this text
   * after a successful click (for ~1.5 s).
   */
  transientLabel?: string
}

export interface MediaActionToolbarProps {
  /** Ordered list of actions to render; caller handles feature detection. */
  actions: MediaAction[]
  /** Extra Tailwind classes for positioning or overrides. */
  className?: string
  /**
   * 'overlay' — floating semi-opaque pill positioned on top of an image.
   * 'bar'     — flat horizontal row for the lightbox bottom/top bar.
   */
  variant?: 'overlay' | 'bar'
}

// ── ActionButton ─────────────────────────────────────────────────────────────
// One button per MediaAction entry. Handles the transient "done" state and
// error toast internally so the parent toolbar stays declarative.

interface ActionButtonProps {
  action: MediaAction
  variant: 'overlay' | 'bar'
}

function ActionButton({ action, variant }: ActionButtonProps) {
  const [done, setDone] = useState(false)
  const [pending, setPending] = useState(false)
  const resetRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (resetRef.current) clearTimeout(resetRef.current)
    }
  }, [])

  const handleClick = async () => {
    if (pending) return
    setPending(true)
    try {
      await Promise.resolve(action.onClick())
      if (action.transientLabel) {
        setDone(true)
        if (resetRef.current) clearTimeout(resetRef.current)
        resetRef.current = setTimeout(() => {
          setDone(false)
        }, 1500)
      }
    } catch (err) {
      // A user who dismisses the native share sheet (or any aborted in-flight
      // request) raises AbortError — that is a deliberate cancel, not a failure,
      // so swallow it silently instead of flashing a red error toast.
      if (err instanceof DOMException && err.name === 'AbortError') {
        return
      }
      const message =
        err instanceof Error ? err.message : 'Action failed'
      useUiStore.getState().addToast({ message, variant: 'error' })
    } finally {
      setPending(false)
    }
  }

  const isOverlay = variant === 'overlay'

  // Base button classes shared across both variants
  const baseClasses = [
    'flex items-center justify-center',
    'min-w-[28px] min-h-[28px]',
    'rounded-md',
    'transition-colors',
    'disabled:opacity-50 disabled:cursor-not-allowed',
    'focus-visible:outline-none',
  ].join(' ')

  const variantClasses = isOverlay
    ? 'text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-1)]/60 px-1.5'
    : 'text-[var(--color-secondary)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-1)] px-2'

  return (
    <button
      type="button"
      aria-label={done && action.transientLabel ? action.transientLabel : action.label}
      title={done && action.transientLabel ? action.transientLabel : action.label}
      onClick={handleClick}
      disabled={pending}
      className={`${baseClasses} ${variantClasses}`}
    >
      {done ? (
        <Check
          size={14}
          weight="bold"
          className="text-[var(--color-success,#22c55e)]"
          aria-hidden
        />
      ) : (
        <span className="flex items-center justify-center" aria-hidden>
          {action.icon}
        </span>
      )}
    </button>
  )
}

// ── MediaActionToolbar ─────────────────────────────────────────────────────────

export function MediaActionToolbar({
  actions,
  className = '',
  variant = 'overlay',
}: MediaActionToolbarProps) {
  if (actions.length === 0) return null

  const isOverlay = variant === 'overlay'

  const containerClasses = isOverlay
    ? [
        'flex items-center gap-0.5 px-1 py-0.5',
        'bg-[var(--color-surface-2)]/85 backdrop-blur-sm',
        'border border-[var(--color-border)]',
        'rounded-lg',
        'shadow-md',
      ].join(' ')
    : 'flex items-center gap-1'

  return (
    <div
      role="toolbar"
      aria-label="Media actions"
      className={`${containerClasses} ${className}`}
    >
      {actions.map((action) => (
        <ActionButton key={action.label} action={action} variant={variant} />
      ))}
    </div>
  )
}
