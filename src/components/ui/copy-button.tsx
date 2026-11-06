import * as React from 'react'
import { Check, Copy, WarningCircle } from '@phosphor-icons/react'
import { IconButton } from './icon-button'
import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip'
import { cn } from '@/lib/utils'

// CopyButton — the kit's copy-to-clipboard control (W2-1 contract §2.8; R41,
// #855 job 2). IconButton + Tooltip. No silent failure: a rejected write, or
// a page where `navigator.clipboard` does not exist (plain-HTTP LAN), shows
// the error state, announces `failedLabel` and hands the error to
// `onCopyError`. Feedback lasts COPY_FEEDBACK_MS, then returns to idle; the
// accessible name stays `label` throughout (the result is announced through
// a polite status region instead of renaming the control).

/** How long the success/failure feedback stays before returning to idle. */
export const COPY_FEEDBACK_MS = 2000

export interface CopyButtonProps {
  /** The text to copy, or a function resolved at click time. */
  value: string | (() => string)
  /** Accessible name and idle tooltip. */
  label?: string
  copiedLabel?: string
  failedLabel?: string
  onCopied?: () => void
  onCopyError?: (error: unknown) => void
  size?: 'sm' | 'default'
  disabled?: boolean
  className?: string
  'data-testid'?: string
}

type CopyState = 'idle' | 'copied' | 'failed'

export function CopyButton({
  value,
  label = 'Copy',
  copiedLabel = 'Copied',
  failedLabel = 'Copy failed',
  onCopied,
  onCopyError,
  size = 'sm',
  disabled = false,
  className,
  'data-testid': testId,
}: CopyButtonProps) {
  const [state, setState] = React.useState<CopyState>('idle')
  const timerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null)

  React.useEffect(() => () => {
    if (timerRef.current !== null) clearTimeout(timerRef.current)
  }, [])

  function showFeedback(next: Exclude<CopyState, 'idle'>) {
    if (timerRef.current !== null) clearTimeout(timerRef.current)
    setState(next)
    timerRef.current = setTimeout(() => {
      timerRef.current = null
      setState('idle')
    }, COPY_FEEDBACK_MS)
  }

  async function handleClick() {
    if (disabled) return
    try {
      const clipboard = typeof navigator === 'undefined' ? undefined : navigator.clipboard
      if (!clipboard) throw new Error('Clipboard access is unavailable on this page')
      await clipboard.writeText(typeof value === 'function' ? value() : value)
    } catch (error) {
      showFeedback('failed')
      onCopyError?.(error)
      return
    }
    showFeedback('copied')
    onCopied?.()
  }

  const announcement = state === 'copied' ? copiedLabel : state === 'failed' ? failedLabel : null
  const tooltipText = announcement ?? label

  return (
    <>
      <Tooltip>
        <TooltipTrigger asChild>
          <IconButton
            aria-label={label}
            size={size}
            disabled={disabled}
            data-testid={testId}
            data-copy-state={state}
            onClick={() => { void handleClick() }}
            className={cn('pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]', className)}
          >
            {state === 'copied' ? (
              <Check data-testid="copy-button-success" aria-hidden="true" className="size-[var(--icon-size-medium)] text-[var(--color-success)]" />
            ) : state === 'failed' ? (
              <WarningCircle data-testid="copy-button-failure" aria-hidden="true" className="size-[var(--icon-size-medium)] text-[var(--color-error)]" />
            ) : (
              <Copy aria-hidden="true" className="size-[var(--icon-size-medium)]" />
            )}
          </IconButton>
        </TooltipTrigger>
        <TooltipContent>{tooltipText}</TooltipContent>
      </Tooltip>
      {announcement !== null ? (
        <span role="status" className="sr-only">{announcement}</span>
      ) : null}
    </>
  )
}
