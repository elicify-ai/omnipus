import { useState, useRef, useCallback, useEffect } from 'react'
import { PaperPlaneRight, Stop, SpinnerGap } from '@phosphor-icons/react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { cn } from '@/lib/utils'

// B3: label union for the stop button state machine.
// 'stop'           — idle streaming, ready to cancel.
// 'stopping'       — cancel clicked (optimistic) or graceful stage received.
// 'force-stopping' — hard stage received; turn is being force-killed.
// 'cancelled'      — detached stage received; shown briefly before revert.
type StopLabel = 'stop' | 'stopping' | 'force-stopping' | 'cancelled'

export function MessageInput() {
  const [value, setValue] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const { sendMessage, cancelStream, isStreaming, cancelStage } = useChatStore()
  const { isConnected } = useConnectionStore()

  // B3: stop-button label state machine.
  // Optimistic: clicking Stop immediately shows 'stopping'.
  // Then synced with cancelStage frames from the gateway.
  const [stopLabel, setStopLabel] = useState<StopLabel>('stop')

  // B3: sync stopLabel with the cancelStage value from the store.
  useEffect(() => {
    if (!isStreaming) {
      // When streaming ends (done frame), revert to default.
      setStopLabel('stop')
      return
    }
    if (cancelStage === 'graceful') {
      setStopLabel('stopping')
    } else if (cancelStage === 'hard') {
      setStopLabel('force-stopping')
    } else if (cancelStage === 'detached') {
      setStopLabel('cancelled')
      // Show 'cancelled' briefly then the button disappears when isStreaming
      // clears on the done frame — no manual revert needed.
    }
    // null → leave whatever label is already showing (may be the optimistic 'stopping').
  }, [cancelStage, isStreaming])

  const handleSend = useCallback(() => {
    const trimmed = value.trim()
    if (!trimmed || isStreaming || !isConnected) return
    sendMessage(trimmed)
    setValue('')
    textareaRef.current?.focus()
  }, [value, isStreaming, isConnected, sendMessage])

  const handleStop = useCallback(() => {
    // Optimistic: show 'stopping' immediately without waiting for the
    // graceful cancel_stage frame (which may take a round-trip).
    setStopLabel('stopping')
    cancelStream()
  }, [cancelStream])

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
    // Escape cancels streaming (no-op when idle) — T14
    if (e.key === 'Escape' && isStreaming) {
      handleStop()
    }
  }

  const handleInput = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setValue(e.target.value)
    // Auto-resize
    const el = e.target
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`
  }

  // FR-21: textarea re-enables when the goroutine is neutered (detached stage).
  const isDetached = cancelStage === 'detached'

  const canSend = value.trim().length > 0 && !isStreaming && isConnected
  const disconnected = !isConnected

  // B3: derive button label text and spinner visibility from stopLabel.
  const stopButtonLabel =
    stopLabel === 'stopping' ? 'Stopping...' :
    stopLabel === 'force-stopping' ? 'Force-stopping...' :
    stopLabel === 'cancelled' ? 'Cancelled' :
    'Stop'
  const showStopSpinner = stopLabel === 'force-stopping'

  return (
    <div className="border-t border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-3">
      {disconnected && (
        <div className="mb-2 text-xs text-[var(--color-error)] flex items-center gap-1">
          <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-error)] inline-block" />
          Disconnected — reconnecting...
        </div>
      )}
      <div
        className={cn(
          'flex items-end gap-2 rounded-xl border bg-[var(--color-surface-2)] px-3 py-2 transition-colors',
          'focus-within:border-[var(--color-accent)]/50 focus-within:ring-1 focus-within:ring-[var(--color-accent)]/20',
          disconnected ? 'border-[var(--color-error)]/30' : 'border-[var(--color-border)]'
        )}
      >
        <textarea
          ref={textareaRef}
          value={value}
          onChange={handleInput}
          onKeyDown={handleKeyDown}
          placeholder={
            disconnected
              ? 'Connecting to gateway...'
              : isStreaming
              ? 'Waiting for response...'
              : 'Message your agent… (Enter to send, Shift+Enter for newline)'
          }
          // FR-21: re-enable input when 'detached' stage arrives (goroutine neutered)
          disabled={disconnected || (isStreaming && !isDetached)}
          rows={1}
          className={cn(
            'flex-1 resize-none bg-transparent text-sm text-[var(--color-secondary)] outline-none placeholder:text-[var(--color-muted)] min-h-[24px] max-h-[200px] leading-6',
            (disconnected || (isStreaming && !isDetached)) && 'opacity-60 cursor-not-allowed'
          )}
          style={{ overflow: 'hidden' }}
          aria-label="Message input"
        />

        {/* Send / Stop button */}
        {isStreaming ? (
          <button
            type="button"
            onClick={handleStop}
            className="shrink-0 h-8 min-w-8 rounded-lg bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30 flex items-center justify-center gap-1.5 px-2 transition-colors"
            aria-label={stopButtonLabel}
            title="Stop (Escape)"
            data-testid="stop-btn"
          >
            {showStopSpinner ? (
              <SpinnerGap size={14} className="animate-spin shrink-0" />
            ) : (
              <Stop size={15} weight="fill" className="shrink-0" />
            )}
            {stopLabel !== 'stop' && (
              <span className="text-xs font-medium whitespace-nowrap">{stopButtonLabel}</span>
            )}
          </button>
        ) : (
          <button
            type="button"
            onClick={handleSend}
            disabled={!canSend}
            className={cn(
              'shrink-0 w-8 h-8 rounded-lg flex items-center justify-center transition-colors',
              canSend
                ? 'bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent-hover)]'
                : 'bg-[var(--color-surface-3)] text-[var(--color-muted)] cursor-not-allowed'
            )}
            aria-label="Send message"
          >
            <PaperPlaneRight size={15} weight="bold" />
          </button>
        )}
      </div>

      <p className="mt-1.5 text-[10px] text-[var(--color-muted)] text-center">
        Agents can make mistakes. Verify important information.
      </p>
    </div>
  )
}
