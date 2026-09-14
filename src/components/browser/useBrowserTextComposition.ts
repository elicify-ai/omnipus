import { useCallback, useEffect, useRef, type ClipboardEvent, type CompositionEvent, type FormEvent, type KeyboardEvent } from 'react'

type Options = {
  identity: () => string | null
  send: (text: string) => boolean
  releaseKeys: () => void
  reportError: (message: string) => void
}

// The editable sink lets the local OS perform composition. Only committed
// text crosses the existing input transport; candidate text stays local.
export function useBrowserTextComposition(options: Options) {
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const optionsRef = useRef(options)
  optionsRef.current = options
  const state = useRef({ active: false, identity: null as string | null, epoch: 0, blocked: false, unblockOnEnd: false, trailing: null as { text: string } | null })
  const cancel = useCallback(() => {
    const s = state.current
    s.active = false; s.identity = null; s.trailing = null; s.blocked = true; s.unblockOnEnd = false; s.epoch++
    if (inputRef.current) inputRef.current.value = ''
  }, [])
  const commit = useCallback((text: string, identity: string | null) => {
    if (!text || !identity) return
    if (Array.from(text).length > 8192) {
      optionsRef.current.reportError('Text is too long to send at once. Paste up to 8,192 characters.')
      return
    }
    const epoch = state.current.epoch
    // Blur can synchronously finish native composition. Defer delivery until
    // that focus transition completes, then recheck exact source ownership.
    queueMicrotask(() => {
      if (state.current.epoch !== epoch || document.activeElement !== inputRef.current || optionsRef.current.identity() !== identity) return
      optionsRef.current.send(text)
    })
  }, [])
  const begin = useCallback(() => {
    const s = state.current
    s.active = true; s.identity = optionsRef.current.identity(); s.blocked = false; s.unblockOnEnd = false; s.trailing = null
  }, [])
  const nativeKey = useCallback((event: KeyboardEvent<HTMLDivElement>) => {
    const s = state.current
    if (s.active && s.identity !== optionsRef.current.identity()) cancel()
    const native = event.nativeEvent
    if (s.active || native.isComposing || event.key === 'Dead' || event.key === 'Process' || event.keyCode === 229) {
      if (!s.active && (!s.blocked || (!native.isComposing && (event.key === 'Dead' || event.key === 'Process' || event.keyCode === 229)))) begin()
      return true
    }
    s.blocked = false; s.trailing = null
    const paste = ((event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === 'v') || (event.shiftKey && !event.ctrlKey && !event.metaKey && event.key === 'Insert')
    const picker = /^Mac/.test(navigator.platform) && event.ctrlKey && event.metaKey && event.code === 'Space'
    return paste || picker
  }, [begin, cancel])
  const onCompositionStart = useCallback(() => {
    if (state.current.blocked || !optionsRef.current.identity() || document.activeElement !== inputRef.current) return
    optionsRef.current.releaseKeys()
    begin()
  }, [begin])
  const onCompositionEnd = useCallback((event: CompositionEvent<HTMLTextAreaElement>) => {
    const s = state.current
    if (!s.active) {
      if (s.unblockOnEnd) { s.blocked = false; s.unblockOnEnd = false }
      s.trailing = { text: event.data }
      const trailing = s.trailing
      queueMicrotask(() => { if (s.trailing === trailing) s.trailing = null })
      event.currentTarget.value = ''
      return
    }
    const identity = s.identity
    s.active = false; s.identity = null; s.trailing = { text: event.data }
    commit(event.data, identity)
    event.currentTarget.value = ''
    const trailing = s.trailing
    queueMicrotask(() => { if (s.trailing === trailing) s.trailing = null })
  }, [commit])
  const onInput = useCallback((event: FormEvent<HTMLTextAreaElement>) => {
    const input = event.nativeEvent as InputEvent
    const s = state.current
    if (input.isComposing || (s.active && input.inputType === 'insertCompositionText')) return
    const text = event.currentTarget.value || input.data || ''
    event.currentTarget.value = ''
    if (s.blocked) return
    if (s.trailing !== null && text === s.trailing.text) { s.trailing = null; return }
    const identity = s.active ? s.identity : optionsRef.current.identity()
    s.active = false; s.identity = null
    commit(text, identity)
  }, [commit])
  const onPaste = useCallback((event: ClipboardEvent<HTMLTextAreaElement>) => {
    event.preventDefault()
    const text = event.clipboardData.getData('text/plain')
    const wasComposing = state.current.active
    cancel()
    state.current.blocked = wasComposing
    state.current.unblockOnEnd = wasComposing
    optionsRef.current.releaseKeys()
    commit(text, optionsRef.current.identity())
  }, [cancel, commit])
  useEffect(() => {
    if (state.current.active && state.current.identity !== options.identity()) cancel()
  })
  useEffect(() => {
    const outside = (event: PointerEvent) => {
      if (event.target instanceof Node && !inputRef.current?.parentElement?.contains(event.target)) cancel()
    }
    document.addEventListener('pointerdown', outside, true)
    return () => { document.removeEventListener('pointerdown', outside, true); cancel() }
  }, [cancel])
  return { inputRef, cancel, nativeKey, onCompositionStart, onCompositionEnd, onInput, onPaste,
    activate: () => { cancel(); state.current.blocked = false },
    onFocus: () => { state.current.blocked = false },
    composing: () => state.current.active,
  }
}
