import { useState, useRef, useCallback, useEffect, useMemo } from 'react'
import { PaperPlaneRight, Stop, SpinnerGap, Microphone, CircleNotch } from '@phosphor-icons/react'
import { useQuery } from '@tanstack/react-query'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'
import { transcribeAudio, isApiError, fetchSkills } from '@/lib/api'
import { cn } from '@/lib/utils'

// B3: label union for the stop button state machine.
// 'stop'           — idle streaming, ready to cancel.
// 'stopping'       — cancel clicked (optimistic) or graceful stage received.
// 'force-stopping' — hard stage received; turn is being force-killed.
// 'cancelled'      — detached stage received; shown briefly before revert.
type StopLabel = 'stop' | 'stopping' | 'force-stopping' | 'cancelled'

// MicState drives the composer mic button (Spec-6 FR-12.1):
// 'idle'        — not recording; click to start.
// 'recording'   — capturing audio; click to stop & transcribe.
// 'transcribing'— audio sent to the transcriber, awaiting text.
type MicState = 'idle' | 'recording' | 'transcribing'

// SlashSuggestion — a single entry in the slash-command autocomplete list.
interface SlashSuggestion {
  trigger: string     // the full command without the leading slash, e.g. "skill web-research"
  label: string       // display label, e.g. "Web Research"
  description?: string
}

export function MessageInput() {
  const [value, setValue] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const { sendMessage, cancelStream, isStreaming, cancelStage } = useChatStore()
  const { isConnected } = useConnectionStore()
  const { addToast } = useUiStore()

  // O11 slash-command autocomplete: fetch skills for the suggestion list.
  // staleTime 60s — skills don't change often mid-session.
  const { data: skills = [] } = useQuery({
    queryKey: ['skills'],
    queryFn: fetchSkills,
    staleTime: 60_000,
    enabled: isConnected,
  })

  // Build the full suggestion list: active skills + a small set of built-in commands.
  const allSuggestions = useMemo<SlashSuggestion[]>(() => {
    const builtIn: SlashSuggestion[] = [
      { trigger: 'recall', label: 'Recall', description: 'Search your memory for relevant context' },
      { trigger: 'remember', label: 'Remember', description: 'Store something in long-term memory' },
      { trigger: 'clear', label: 'Clear session', description: 'Start a new session in this chat' },
    ]
    const skillSuggestions: SlashSuggestion[] = skills
      .filter((s) => s.status === 'active')
      .map((s) => ({
        trigger: `skill ${s.id}`,
        label: s.name,
        description: s.description,
      }))
    return [...builtIn, ...skillSuggestions]
  }, [skills])

  // Slash-command autocomplete state.
  const [slashQuery, setSlashQuery] = useState<string | null>(null)
  const [slashIndex, setSlashIndex] = useState(0)

  const slashSuggestions = useMemo<SlashSuggestion[]>(() => {
    if (slashQuery === null) return []
    const q = slashQuery.toLowerCase()
    return allSuggestions.filter(
      (s) => s.trigger.toLowerCase().includes(q) || s.label.toLowerCase().includes(q),
    )
  }, [slashQuery, allSuggestions])

  const applySlashSuggestion = useCallback(
    (suggestion: SlashSuggestion) => {
      setValue(`/${suggestion.trigger} `)
      setSlashQuery(null)
      setSlashIndex(0)
      // Focus after selecting
      setTimeout(() => textareaRef.current?.focus(), 0)
    },
    [],
  )

  // Composer mic state + MediaRecorder plumbing.
  const [micState, setMicState] = useState<MicState>('idle')
  const mediaRecorderRef = useRef<MediaRecorder | null>(null)
  const audioChunksRef = useRef<Blob[]>([])
  const mediaStreamRef = useRef<MediaStream | null>(null)
  // S4: guard onstop after onerror — MediaRecorder may still fire onstop
  // after an onerror, which would otherwise transcribe partial/garbage
  // chunks. Set in onerror, checked at the top of onstop.
  const recorderErroredRef = useRef(false)

  // Append transcribed text to the current input, inserting a space when needed.
  const appendTranscript = useCallback((text: string) => {
    const trimmed = text.trim()
    if (!trimmed) return
    setValue((prev) => (prev.trim() ? `${prev.trimEnd()} ${trimmed}` : trimmed))
    textareaRef.current?.focus()
  }, [])

  // Stop the mic stream tracks (releases the OS mic indicator).
  const releaseStream = useCallback(() => {
    mediaStreamRef.current?.getTracks().forEach((t) => t.stop())
    mediaStreamRef.current = null
  }, [])

  const startRecording = useCallback(async () => {
    if (typeof navigator === 'undefined' || !navigator.mediaDevices?.getUserMedia) {
      addToast({ message: 'Voice input is not supported in this browser.', variant: 'error' })
      return
    }
    if (typeof MediaRecorder === 'undefined') {
      addToast({ message: 'Voice input is not supported in this browser.', variant: 'error' })
      return
    }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      mediaStreamRef.current = stream
      audioChunksRef.current = []
      recorderErroredRef.current = false
      const recorder = new MediaRecorder(stream)
      mediaRecorderRef.current = recorder
      recorder.ondataavailable = (e) => {
        if (e.data.size > 0) audioChunksRef.current.push(e.data)
      }
      recorder.onerror = (ev: Event) => {
        // S4: mark errored so the subsequent onstop bails out instead of
        // transcribing partial/garbage chunks.
        recorderErroredRef.current = true
        audioChunksRef.current = []
        // Surface non-fatal MediaRecorder failures so the user isn't left in
        // a stuck "recording" state. The event is an MediaRecorderErrorEvent
        // in compliant browsers, but the DOM type is just Event in some TS
        // versions — read the message safely.
        releaseStream()
        setMicState('idle')
        const message =
          ('error' in ev && (ev as { error?: Error }).error?.message) ||
          ('message' in ev && typeof (ev as { message?: string }).message === 'string'
            ? (ev as { message?: string }).message
            : 'Recording failed')
        addToast({ message: `Recording failed: ${message}`, variant: 'error' })
      }
      recorder.onstop = async () => {
        // S4: if onerror already fired, drop this stop and reset state —
        // the error path already released the stream and reset micState.
        if (recorderErroredRef.current) {
          recorderErroredRef.current = false
          releaseStream()
          setMicState('idle')
          return
        }
        releaseStream()
        const chunks = audioChunksRef.current
        audioChunksRef.current = []
        if (chunks.length === 0) {
          setMicState('idle')
          return
        }
        const blob = new Blob(chunks, { type: recorder.mimeType || 'audio/webm' })
        setMicState('transcribing')
        try {
          const result = await transcribeAudio(blob)
          appendTranscript(result.text)
        } catch (err) {
          const message = isApiError(err)
            ? err.status === 503
              ? 'No voice transcriber configured. Add one in Settings → Integrations.'
              : err.userMessage
            : err instanceof Error
              ? err.message
              : 'Transcription failed'
          addToast({ message, variant: 'error' })
        } finally {
          setMicState('idle')
        }
      }
      recorder.start()
      setMicState('recording')
    } catch (err) {
      releaseStream()
      setMicState('idle')
      // getUserMedia rejects with NotAllowedError when the user denies permission.
      const denied = err instanceof DOMException && (err.name === 'NotAllowedError' || err.name === 'SecurityError')
      addToast({
        message: denied
          ? 'Microphone access was denied. Enable it in your browser to use voice input.'
          : 'Could not start recording.',
        variant: 'error',
      })
    }
  }, [addToast, appendTranscript, releaseStream])

  const stopRecording = useCallback(() => {
    const recorder = mediaRecorderRef.current
    if (recorder && recorder.state !== 'inactive') {
      recorder.stop() // triggers onstop → transcription
    } else {
      releaseStream()
      setMicState('idle')
    }
  }, [releaseStream])

  const handleMicClick = useCallback(() => {
    if (micState === 'recording') {
      stopRecording()
    } else if (micState === 'idle') {
      void startRecording()
    }
    // 'transcribing' → button disabled, no-op.
  }, [micState, startRecording, stopRecording])

  // Release the mic stream on unmount so the OS indicator clears.
  useEffect(() => releaseStream, [releaseStream])

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
    // O11 slash-command navigation: Arrow keys / Enter / Escape when the menu is open.
    if (slashQuery !== null && slashSuggestions.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setSlashIndex((i) => Math.min(i + 1, slashSuggestions.length - 1))
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setSlashIndex((i) => Math.max(i - 1, 0))
        return
      }
      if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
        e.preventDefault()
        applySlashSuggestion(slashSuggestions[slashIndex])
        return
      }
      if (e.key === 'Escape') {
        setSlashQuery(null)
        return
      }
    }

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
    const newValue = e.target.value
    setValue(newValue)
    // Auto-resize
    const el = e.target
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`

    // O11 slash-command detection: activate autocomplete when the composer
    // starts with "/" and the caret is still within the command portion.
    // A trailing space after the "/" word means the user has finished typing
    // the command — close the menu.
    if (newValue.startsWith('/')) {
      const query = newValue.slice(1)
      // Hide if query contains a space (command completed or text body started)
      if (query.includes(' ')) {
        setSlashQuery(null)
      } else {
        setSlashQuery(query)
        setSlashIndex(0)
      }
    } else {
      setSlashQuery(null)
    }
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
    <div className="border-t border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-3 relative">
      {/* O11 slash-command autocomplete popover — renders above the composer */}
      {slashQuery !== null && slashSuggestions.length > 0 && (
        <div
          role="listbox"
          aria-label="Slash command suggestions"
          className="absolute bottom-full left-4 right-4 mb-1 z-20 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] shadow-lg overflow-hidden max-h-52 overflow-y-auto"
        >
          {slashSuggestions.map((suggestion, idx) => (
            <button
              key={suggestion.trigger}
              type="button"
              role="option"
              aria-selected={idx === slashIndex}
              onClick={() => applySlashSuggestion(suggestion)}
              className={cn(
                'flex w-full items-start gap-2 px-3 py-2 text-left text-xs transition-colors',
                idx === slashIndex
                  ? 'bg-[var(--color-surface-2)] text-[var(--color-secondary)]'
                  : 'text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
              )}
            >
              <span
                className="shrink-0 text-[var(--color-accent)] font-mono font-semibold"
                aria-hidden="true"
              >
                /{suggestion.trigger}
              </span>
              <span className="min-w-0">
                <span className="block font-medium truncate">{suggestion.label}</span>
                {suggestion.description && (
                  <span className="block text-[var(--color-muted)] truncate">{suggestion.description}</span>
                )}
              </span>
            </button>
          ))}
        </div>
      )}
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

        {/* Composer mic — voice input via the active transcriber (Spec-6 FR-12.1).
            Hidden while streaming (the Stop button takes the slot). */}
        {!isStreaming && (
          <button
            type="button"
            onClick={handleMicClick}
            disabled={disconnected || micState === 'transcribing'}
            aria-label={
              micState === 'recording'
                ? 'Stop recording and transcribe'
                : micState === 'transcribing'
                ? 'Transcribing…'
                : 'Start voice input'
            }
            aria-pressed={micState === 'recording'}
            title={micState === 'recording' ? 'Stop recording' : 'Voice input'}
            data-testid="mic-btn"
            className={cn(
              'shrink-0 w-8 h-8 rounded-lg flex items-center justify-center transition-colors',
              micState === 'recording'
                ? 'bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30 animate-pulse'
                : micState === 'transcribing'
                ? 'bg-[var(--color-surface-3)] text-[var(--color-muted)] cursor-wait'
                : 'bg-[var(--color-surface-3)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] disabled:opacity-50 disabled:cursor-not-allowed'
            )}
          >
            {micState === 'transcribing' ? (
              <CircleNotch size={15} className="animate-spin" />
            ) : (
              <Microphone size={15} weight={micState === 'recording' ? 'fill' : 'regular'} />
            )}
          </button>
        )}

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
