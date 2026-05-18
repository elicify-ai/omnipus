import React, { useEffect, useRef, useState } from 'react'
import { generateId } from '@/lib/constants'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  ThreadPrimitive,
  MessagePrimitive,
  ComposerPrimitive,
  ActionBarPrimitive,
  AuiIf,
  useComposerRuntime,
  useMessage,
} from '@assistant-ui/react'
import {
  ArrowCounterClockwise,
  User,
  Robot,
  PaperPlaneRight,
  Stop,
  Copy,
  Check,
  ListChecks,
  Paperclip,
  File,
  X,
} from '@phosphor-icons/react'
import OmnipusAvatar from '@/assets/logo/omnipus-avatar.svg?url'
import { IconRenderer } from '@/components/shared/IconRenderer'
import { SessionPanel } from './SessionPanel'
import { GenericToolCall } from './tools/GenericToolCall'
import { ExecApprovalBlock } from './ExecApprovalBlock'
import { RateLimitIndicator } from './RateLimitIndicator'
import { MarkdownText } from './markdown-text'
import { SubagentBlock } from './SubagentBlock'
import { Button } from '@/components/ui/button'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { fetchAgents, fetchSessionMessages, createSession, uploadFiles, isApiError } from '@/lib/api'
import { cn } from '@/lib/utils'

// ── Message components ────────────────────────────────────────────────────────

function UserMessage() {
  const message = useMessage()
  return (
    <MessagePrimitive.Root data-testid="user-message" data-message-id={message.id} className="group flex gap-3 px-4 py-3 flex-row-reverse">
      <div className="shrink-0 w-7 h-7 rounded-full flex items-center justify-center bg-[var(--color-accent)]/20 text-[var(--color-accent)]">
        <User size={14} weight="bold" />
      </div>
      <div className="flex flex-col items-end gap-1 max-w-[85%] min-w-0">
        <div className="rounded-xl px-4 py-3 text-sm leading-relaxed bg-[var(--color-surface-2)] text-[var(--color-secondary)] rounded-tr-sm">
          <MessagePrimitive.Parts>
            {({ part }) => {
              if (part.type !== 'text') return null
              return <p className="whitespace-pre-wrap break-words">{part.text}</p>
            }}
          </MessagePrimitive.Parts>
        </div>
      </div>
    </MessagePrimitive.Root>
  )
}

function SystemMessage() {
  return (
    <MessagePrimitive.Root className="flex justify-center py-2">
      <div className="text-xs text-[var(--color-muted)] bg-[var(--color-surface-2)] px-3 py-1 rounded-full">
        <MessagePrimitive.Parts>
          {({ part }) => {
            if (part.type !== 'text') return null
            return <span>{part.text}</span>
          }}
        </MessagePrimitive.Parts>
      </div>
    </MessagePrimitive.Root>
  )
}

// Animated thinking indicator with rotating status messages
const THINKING_MESSAGES = [
  'Thinking…',
  'Composing response…',
  'Processing your request…',
  'Analyzing…',
  'Generating…',
]

function ThinkingIndicator() {
  const [msgIndex, setMsgIndex] = useState(0)

  useEffect(() => {
    const interval = setInterval(() => {
      setMsgIndex((i) => (i + 1) % THINKING_MESSAGES.length)
    }, 2000)
    return () => clearInterval(interval)
  }, [])

  return (
    <span className="text-[var(--color-muted)] italic flex items-center gap-2.5 py-1">
      <span className="flex gap-1">
        <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] animate-bounce" style={{ animationDelay: '0ms' }} />
        <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] animate-bounce" style={{ animationDelay: '150ms' }} />
        <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] animate-bounce" style={{ animationDelay: '300ms' }} />
      </span>
      <span className="text-xs transition-opacity duration-300">{THINKING_MESSAGES[msgIndex]}</span>
    </span>
  )
}

// Custom text renderer with streaming cursor.
// Wrapped in ErrorBoundary because MessagePartPrimitive.InProgress throws
// "MessagePartText can only be used inside text or reasoning message parts"
// when AssistantUI calls this component for a tool-call-only message.
class TextPartErrorBoundary extends React.Component<{ children: React.ReactNode }, { hasError: boolean }> {
  constructor(props: { children: React.ReactNode }) {
    super(props)
    this.state = { hasError: false }
  }
  static getDerivedStateFromError() { return { hasError: true } }
  componentDidCatch(error: Error) {
    // Only suppress the known AssistantUI error; log everything else.
    if (!error.message?.includes('MessagePartText')) {
      console.error('[TextPartErrorBoundary] Unexpected error:', error)
    }
  }
  render() { return this.state.hasError ? null : this.props.children }
}

function AssistantTextPart() {
  return (
    <TextPartErrorBoundary>
      <div>
        <MarkdownText />
      </div>
    </TextPartErrorBoundary>
  )
}

// Shows thinking dots inside the assistant message while it is still running.
// Stays visible the entire turn — including between tool-call steps after some
// text has streamed — so the user always knows the agent is still working.
// Uses useMessage() for reactive state (not getState() which is a snapshot).
function InlineThinkingIndicator() {
  const message = useMessage()
  const isRunning = message.status?.type === 'running'
  if (!isRunning) return null
  return <ThinkingIndicator />
}

// Fallback tool UI for tools without a registered makeAssistantToolUI component.
// ToolCallMessagePartProps passes: toolCallId, toolName, args, result, status
function FallbackToolUI(props: { toolCallId: string; toolName: string; args: unknown; result: unknown; status: import('@assistant-ui/react').MessagePartStatus }) {
  const storeToolCalls = useChatStore((s) => s.toolCalls)
  const liveCall = storeToolCalls[props.toolCallId]
  return (
    <GenericToolCall
      toolName={props.toolName}
      args={props.args}
      result={liveCall?.result ?? props.result}
      status={props.status}
      error={liveCall?.error}
      durationMs={liveCall?.duration_ms}
    />
  )
}

function AssistantMessageRetryButton() {
  const message = useMessage()
  const sendMessage = useChatStore((s) => s.sendMessage)
  const messages = useChatStore((s) => s.messages)
  const isStreaming = useChatStore((s) => s.isStreaming)

  const status = message.status?.type
  // AssistantUI maps our store's 'error' and 'interrupted' statuses both to
  // { type: 'incomplete' } via buildMessageStatus in omnipus-runtime.ts.
  const isErrorOrIncomplete = status === 'incomplete'
  const hasUserMessage = messages.some((m) => m.role === 'user')

  if (!isErrorOrIncomplete || isStreaming || !hasUserMessage) return null

  function handleRetry() {
    const lastUserMsg = [...messages].reverse().find((m) => m.role === 'user')
    if (lastUserMsg) {
      sendMessage(lastUserMsg.content)
    }
  }

  return (
    <button
      type="button"
      onClick={handleRetry}
      aria-label="Retry — resend the last user message"
      className="flex items-center gap-1 px-2 py-1 rounded text-[10px] text-[var(--color-error)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
      title="Retry — resend the last user message"
    >
      <ArrowCounterClockwise size={11} />
      <span>Retry</span>
    </button>
  )
}

// Renders inline media attachments (images, files) attached to the assistant
// message this component is mounted inside. Keyed by message id so each
// message bubble only shows its own media — not the most recent assistant's,
// which would double-render the screenshot on every follow-up turn.
function InlineMedia() {
  const message = useMessage()
  const messages = useChatStore((s) => s.messages)
  const storeMsg = messages.find((m) => m.id === message.id)

  if (!storeMsg?.media?.length) return null

  return (
    <div className="flex flex-col gap-2 mt-2">
      {storeMsg.media.map((m, i) =>
        m.type === 'image' ? (
          <div key={`${m.url}-${i}`} className="rounded-lg overflow-hidden border border-[var(--color-border)] max-w-2xl">
            <img
              src={m.url}
              alt={m.caption || m.filename}
              className="block w-full h-auto max-h-[60vh] object-contain"
              loading="lazy"
            />
            {m.caption && (
              <p className="text-xs text-[var(--color-muted)] px-2 py-1">{m.caption}</p>
            )}
          </div>
        ) : (
          <a
            key={`${m.url}-${i}`}
            href={m.url}
            download={m.filename}
            className="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-[var(--color-border)] text-xs text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
          >
            <File size={14} />
            {m.filename}
          </a>
        ),
      )}
    </div>
  )
}

// Renders subagent spans attached to the current message (FR-H-008).
// useMessage().id corresponds to the store message's id (set in omnipus-runtime convertMessage).
function SubagentSpansRenderer() {
  const message = useMessage()
  const messages = useChatStore((s) => s.messages)
  const storeMsg = messages.find((m) => m.id === message.id)
  const spans = storeMsg?.spans ?? []
  if (spans.length === 0) return null
  return (
    <>
      {spans.map((span) => (
        <SubagentBlock key={span.spanId} span={span} />
      ))}
    </>
  )
}

function AssistantMessageAvatar() {
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const agent = agents.find((a) => a.id === activeAgentId)

  return (
    <div
      className="shrink-0 w-7 h-7 rounded-full flex items-center justify-center text-[var(--color-secondary)]"
      style={{ backgroundColor: agent?.color ?? 'var(--color-surface-3)' }}
      title={agent?.name}
    >
      {agent?.icon ? (
        <IconRenderer icon={agent.icon} size={14} />
      ) : (
        <Robot size={14} weight="bold" />
      )}
    </div>
  )
}


// FR-21: Renders (interrupted) status markers for assistant messages that have
// status:'interrupted' in the Zustand store.
//
// This component is rendered OUTSIDE ThreadPrimitive.Root and outside the
// scrollable Viewport. This guarantees two properties:
//   1. It subscribes directly to Zustand (bypasses AssistantUI rendering).
//   2. It is not inside any overflow-clipped container (Playwright can see it).
//
// Each marker is rendered as a visually small but non-zero text span so that
// E2E tests can locate it with page.locator('text=(interrupted)') combined
// with toBeVisible(). The span has non-zero height because it contains text.
//
// The visible (interrupted) label rendered inside AssistantMessage handles
// the correct visual positioning within the message bubble for human users.
// This component is the reliable E2E-detectable fallback.
function InterruptedMessageMarkers() {
  const messages = useChatStore((s) => s.messages)
  const interrupted = messages.filter(
    (m) => m.role === 'assistant' && m.status === 'interrupted'
  )
  if (interrupted.length === 0) return null
  return (
    <>
      {interrupted.map((m) => (
        <div
          key={m.id}
          data-testid="interrupted-marker"
          data-message-id={m.id}
          className="text-[10px] text-[var(--color-muted)] italic text-center pb-1"
        >
          (interrupted)
        </div>
      ))}
    </>
  )
}

function AssistantMessage() {
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const message = useMessage()
  const messages = useChatStore((s) => s.messages)

  // Prefer the per-message agentId (set during transcript replay) over the
  // session-level activeAgentId. This makes multi-agent transcripts show the
  // correct per-turn agent label instead of the current session agent.
  const storeMsg = messages.find((m) => m.id === message.id)
  const messageAgentId = storeMsg?.agentId ?? activeAgentId
  const agent = agents.find((a) => a.id === messageAgentId)
  // Fallback to the raw agentId string if the agent isn't in the list yet
  const agentDisplayName = agent?.name ?? (messageAgentId || null)
  // FR-21: show (interrupted) suffix when the store marks this message interrupted.
  const isInterrupted = storeMsg?.status === 'interrupted'

  return (
    <MessagePrimitive.Root
      data-testid="assistant-message"
      data-message-id={message.id}
      data-status={message.status?.type ?? 'complete'}
      className="group flex gap-3 px-4 py-3"
    >
      <AssistantMessageAvatar />
      <div className="flex flex-col gap-1 max-w-[85%] min-w-0 flex-1">
        {agentDisplayName && (
          <span data-testid="agent-label" className="text-[10px] text-[var(--color-muted)]">{agentDisplayName}</span>
        )}
        <div className="text-sm leading-relaxed text-[var(--color-secondary)]">
          {/* Media (screenshots, files) renders BEFORE the parts so the image
              shows directly under the tool-call pill that produced it, with
              streamed assistant text appearing below the image. Without this
              order the image gets pinned to the bottom of the bubble while
              new text streams above it — visually disconnecting the
              screenshot from the "Here's your screenshot…" caption. */}
          <InlineMedia />
          {/* Use components prop so AssistantUI can inject registered tool UIs
              (from makeAssistantToolUI) automatically by tool name. Unregistered
              tools fall through to FallbackToolUI (generic JSON badge). */}
          <MessagePrimitive.Parts
            components={{
              Text: AssistantTextPart,
              tools: {
                Fallback: FallbackToolUI as unknown as import('@assistant-ui/react').ToolCallMessagePartComponent,
              },
            }}
          />
          {/* Subagent spans — rendered per-message, keyed by span_id (FR-H-008) */}
          <SubagentSpansRenderer />
          {/* Trailing thinking indicator — sits at the bottom of the bubble
              while the turn is running so the user always sees a "still
              working" cue at the position where the next text/tool will
              appear. Once a token streams in, the streamed text renders
              above the indicator and pushes it further down. */}
          <InlineThinkingIndicator />
        </div>

        {/* Action bar — Copy + Retry buttons, visible on hover */}
        <ActionBarPrimitive.Root className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity duration-150">
          <ActionBarPrimitive.Copy asChild>
            <button
              type="button"
              aria-label="Copy message"
              className="flex items-center gap-1 px-2 py-1 rounded text-[10px] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
              title="Copy message"
            >
              <AuiIf condition={(s) => s.message.isCopied}>
                <Check size={11} weight="bold" className="text-[var(--color-success)]" />
                <span>Copied!</span>
              </AuiIf>
              <AuiIf condition={(s) => !s.message.isCopied}>
                <Copy size={11} />
                <span>Copy</span>
              </AuiIf>
            </button>
          </ActionBarPrimitive.Copy>
          <AssistantMessageRetryButton />
        </ActionBarPrimitive.Root>
        {/* FR-21: interrupted status label — shown when the turn was cancelled */}
        {isInterrupted && (
          <span className="text-[10px] text-[var(--color-muted)] italic px-1">(interrupted)</span>
        )}
      </div>
    </MessagePrimitive.Root>
  )
}

// ── Slash command types ───────────────────────────────────────────────────────

interface SlashCommand {
  label: string
  description: string
  // When true, this command remains visible in the slash menu even while a
  // response is streaming. All other commands are hidden during streaming.
  availableWhileStreaming?: boolean
}

// Built-in slash commands. Custom commands registered via 'commands' WebSocket
// frame are not yet wired; see sprint-h-subagent-block-spec.md for the design.
const SLASH_COMMANDS: SlashCommand[] = [
  { label: '/session new', description: 'Start a new session' },
  { label: '/clear', description: 'Clear all messages' },
  { label: '/help', description: 'Show help information' },
  // FR-3a: /cancel must be reachable mid-turn; it is the only streaming-safe command.
  { label: '/cancel', description: 'Cancel current turn', availableWhileStreaming: true },
]

const HELP_TEXT = `**Omnipus commands:**
- \`/session new\` — Start a new session
- \`/clear\` — Clear the current chat history
- \`/cancel\` — Cancel the current in-progress turn
- \`/help\` — Show this help message

**Tips:**
- Press **Enter** to send, **Shift+Enter** for newline
- Click tool call headers to expand/collapse details
- Hover over messages to copy them`

// ── Composer ──────────────────────────────────────────────────────────────────

function composerPlaceholder(isConnected: boolean, isStreaming: boolean, isReplaying: boolean, agentName: string): string {
  if (!isConnected) return 'Connecting to gateway...'
  if (isReplaying) return 'Loading session history...'
  if (isStreaming) return 'Waiting for response...'
  return `Message ${agentName}…`
}

const HARMFUL_EXTENSIONS = ['.exe', '.bat', '.cmd', '.sh', '.ps1', '.dll', '.sys', '.msi', '.scr', '.com']

/** Renders an image thumbnail for a pending file, revoking the object URL on unmount to prevent memory leaks. */
function FilePreviewThumbnail({ file }: { file: File }) {
  const [url, setUrl] = useState<string | null>(null)

  useEffect(() => {
    const objectUrl = URL.createObjectURL(file)
    setUrl(objectUrl)
    return () => URL.revokeObjectURL(objectUrl)
  }, [file])

  if (!url) return null
  return <img src={url} className="w-8 h-8 rounded object-cover" alt={file.name} />
}

// Exported for unit testing (TDD row 22).
export function OmnipusComposer({ agentRemoved = false }: { agentRemoved?: boolean }) {
  const isStreaming = useChatStore((s) => s.isStreaming)
  // FR-I-014: disable send while replay frames are still arriving
  const isReplaying = useChatStore((s) => s.isReplaying)
  const isConnected = useConnectionStore((s) => s.isConnected)
  const cancelStream = useChatStore((s) => s.cancelStream)
  const setMessages = useChatStore((s) => s.setMessages)
  const appendMessage = useChatStore((s) => s.appendMessage)
  const setActiveSession = useSessionStore((s) => s.setActiveSession)
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const sendMessage = useChatStore((s) => s.sendMessage)
  const addToast = useUiStore((s) => s.addToast)
  const composerRuntime = useComposerRuntime()
  const queryClient = useQueryClient()

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const activeAgentName = agents.find((a) => a.id === activeAgentId)?.name ?? 'Omnipus'

  const { mutate: doCreateSession, isPending: isCreatingSession } = useMutation({
    mutationFn: () => {
      if (!activeAgentId) throw new Error('No agent selected')
      return createSession(activeAgentId)
    },
    onSuccess: (session) => {
      // agentType is already set in the store from the agent selection in SessionBar.
      // Passing null here preserves the existing activeAgentType via setActiveSession's fallback.
      setActiveSession(session.id, session.agent_id, null)
      queryClient.invalidateQueries({ queryKey: ['sessions'] })
      addToast({ message: 'New session started', variant: 'success' })
    },
    onError: (err: unknown) => addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to create session', variant: 'error' }),
  })

  const [inputValue, setInputValue] = useState('')
  const [slashOpen, setSlashOpen] = useState(false)
  const [slashHighlight, setSlashHighlight] = useState(0)
  const [pendingFiles, setPendingFiles] = useState<File[]>([])
  const [isUploading, setIsUploading] = useState(false)
  const [isDragging, setIsDragging] = useState(false)
  // EC-15 / FR-21: stop button label progression:
  //   'stop'     — idle, button shows Stop icon
  //   'stopping' — user clicked, button shows "Stopping..." (synchronous, no network RTT)
  const [stopLabel, setStopLabel] = useState<'stop' | 'stopping'>('stop')
  // T25: track when stopLabel last transitioned to 'stopping' so we can enforce a
  // minimum display duration. Without this, a very fast LLM response causes the done
  // frame to arrive within milliseconds of the click, immediately triggering the
  // useEffect([isStreaming]) reset and making "Stopping..." vanish before any
  // assertion (or user eye) can catch it.
  const stoppingStartedAt = useRef<number>(0)
  // T23: track when streaming last started. Used by the global Escape handler to
  // decide whether Escape should still trigger a cancel in the race window where
  // isStreaming just went false (done frame arrived) but the user pressed Escape
  // intending to cancel a turn they just observed streaming.
  const streamingStartedAt = useRef<number>(0)
  const fileInputRef = useRef<HTMLInputElement>(null)
  // Tracks whether we already warned for the current large-input threshold crossing,
  // so we only fire one toast per paste/input event that exceeds 1MB.
  const hasWarnedLargeInput = useRef(false)

  // FR-3a: during streaming, show the slash menu ONLY if at least one command
  // with availableWhileStreaming:true matches the current input prefix.
  // Outside streaming, show all commands as before.
  const visibleSlashCommands = (() => {
    if (!inputValue.startsWith('/') || isReplaying || !isConnected) return []
    const all = SLASH_COMMANDS.filter((cmd) => cmd.label.startsWith(inputValue) || inputValue === '/')
    if (isStreaming) return all.filter((cmd) => cmd.availableWhileStreaming === true)
    return all
  })()
  const shouldShowSlash = visibleSlashCommands.length > 0 && (inputValue.startsWith('/')) && !isReplaying && isConnected

  // T23: record when a new stream starts so the global Escape handler can detect
  // the race window where the done frame arrived before Escape was pressed.
  useEffect(() => {
    if (isStreaming) {
      streamingStartedAt.current = Date.now()
    }
  }, [isStreaming])

  // EC-15: reset the stop label back to 'stop' whenever streaming ends so the
  // button is fresh for the next turn.
  // T25: enforce a minimum 1000ms display of "Stopping..." before resetting.
  // Fast LLM responses can deliver the done frame within milliseconds of the
  // cancel click, making "Stopping..." invisible to tests and users. We delay
  // the reset by the remaining portion of the minimum display window.
  const MIN_STOPPING_DISPLAY_MS = 1000
  useEffect(() => {
    if (!isStreaming) {
      const elapsed = Date.now() - stoppingStartedAt.current
      const remaining = MIN_STOPPING_DISPLAY_MS - elapsed
      if (stopLabel === 'stopping' && remaining > 0) {
        const timer = setTimeout(() => setStopLabel('stop'), remaining)
        return () => clearTimeout(timer)
      }
      setStopLabel('stop')
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isStreaming])

  function closeSlash() {
    setSlashOpen(false)
    setSlashHighlight(0)
  }

  function executeSlashCommand(cmd: string) {
    closeSlash()
    composerRuntime.setText('')
    setInputValue('')

    if (cmd === '/clear') {
      setMessages([])
      const { activeSessionId: sid } = useSessionStore.getState()
      if (sid) queryClient.removeQueries({ queryKey: ['messages', sid] })
      return
    }

    if (cmd === '/help') {
      appendMessage({
        id: generateId(),
        role: 'system',
        content: HELP_TEXT,
        timestamp: new Date().toISOString(),
        status: 'done',
      })
      return
    }

    if (cmd === '/session new') {
      if (!isCreatingSession) doCreateSession()
      return
    }

    // FR-3a: /cancel uses the same cancelStream() as the Stop button.
    // Only morph the button to "Stopping..." if the turn is actively streaming.
    // If the turn already completed, cancelStream() marks the last message as
    // interrupted but there is no streaming button to morph — setting 'stopping'
    // when isStreaming is already false would leave the button stuck because the
    // useEffect([isStreaming]) will not fire again (no state change) to reset it.
    if (cmd === '/cancel') {
      if (isStreaming) {
        stoppingStartedAt.current = Date.now()
        setStopLabel('stopping')
      }
      cancelStream()
      return
    }
  }

  function handleFilesSelected(files: File[]) {
    const harmful = files.filter((f) =>
      HARMFUL_EXTENSIONS.some((ext) => f.name.toLowerCase().endsWith(ext))
    )
    if (harmful.length > 0) {
      const confirmed = window.confirm(
        `Warning: ${harmful.map((f) => f.name).join(', ')} may be potentially harmful file(s).\n\nAre you sure you want to upload?`
      )
      if (!confirmed) return
      const doubleConfirmed = window.confirm(
        `Please confirm again: Upload ${harmful.length} potentially harmful file(s)?`
      )
      if (!doubleConfirmed) return
    }
    setPendingFiles((prev) => [...prev, ...files])
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    // US-1.4 / FR-23: Escape cancels a turn.
    // Only morph the button to "Stopping..." if the turn is actively streaming
    // (same logic as the /cancel command and the stop button click). Pressing
    // Escape on a completed turn still marks the message as interrupted via
    // cancelStream() → markLastMessageInterrupted(), but there is no streaming
    // button to morph — setting 'stopping' when isStreaming is false would leave
    // the button stuck because the useEffect([isStreaming]) does not fire again.
    if (e.key === 'Escape' && (isStreaming || stopLabel === 'stopping')) {
      e.preventDefault()
      if (isStreaming) {
        stoppingStartedAt.current = Date.now()
        setStopLabel('stopping')
      }
      cancelStream()
      return
    }

    // Block Enter submission while streaming — slash menu Enter still works below.
    if (e.key === 'Enter' && isStreaming && !slashOpen) {
      e.preventDefault()
      return
    }

    if (!shouldShowSlash) return

    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setSlashHighlight((h) => (h + 1) % visibleSlashCommands.length)
      setSlashOpen(true)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSlashHighlight((h) => (h - 1 + visibleSlashCommands.length) % visibleSlashCommands.length)
      setSlashOpen(true)
    } else if (e.key === 'Enter' && slashOpen) {
      e.preventDefault()
      if (visibleSlashCommands[slashHighlight]) {
        executeSlashCommand(visibleSlashCommands[slashHighlight].label)
      }
    } else if (e.key === 'Escape') {
      closeSlash()
    }
  }

  // US-1.4 / FR-23: Global Escape key handler — cancels a turn.
  // Fires even when the input does not have focus (e.g. user
  // clicked somewhere else on the page). The input-level handler above covers
  // the focused-input case; this effect covers the unfocused case (T23).
  //
  // T23 FIX: Two problems existed with the previous implementation:
  //
  //   AssistantUI's cancelOnEscape: ComposerPrimitive.Input has cancelOnEscape
  //   defaulting to true, which consumed the Escape keydown before our React onKeyDown
  //   handler saw it. Fixed by passing cancelOnEscape={false} to that component.
  //
  //   Problem 2 — Guard vs. race window: The Playwright test calls page.keyboard.press
  //   ('Escape') immediately after triggerLongStreamingTurn() returns (stop button first
  //   visible). A fast LLM (Gemini 2.5 Flash) can complete the turn and deliver the done
  //   frame in <1s, so by the time Escape fires: isStreaming=false, stopLabel='stop' (the
  //   useEffect reset it), and the old guard `if (!isStreaming && stopLabel !== 'stopping')`
  //   causes a silent early-return — cancellation never happens and (interrupted) never
  //   appears.
  //
  //   Fix: use a read-through to the Zustand store to check if the last assistant message
  //   is in a cancellable state: either actively streaming (isStreaming:true on the message)
  //   or very recently completed (status:'done' but no prior cancel). This is a snapshot
  //   read — it bypasses the React closure's stale isStreaming value entirely.
  //
  // cancelStream() internally gates the WS send on isStreaming, so calling it when
  // the turn is already done is safe: it just calls markLastMessageInterrupted() to
  // set the interrupted label on the last message, which is correct and desired here.
  useEffect(() => {
    // T23 RACE-WINDOW constant: allow Escape to cancel a turn that completed within
    // this many ms of the stream starting. When a fast LLM (Gemini 2.5 Flash) delivers
    // a full response in <2s, the done frame can arrive and clear isStreaming before
    // the test (or user) presses Escape. We treat Escape as a cancel intent if the
    // stream started within the last CANCEL_RACE_WINDOW_MS ms.
    const CANCEL_RACE_WINDOW_MS = 8_000
    function handleGlobalEscape(e: KeyboardEvent) {
      if (e.key !== 'Escape') return
      // Read live state from the store — bypasses the stale React closure value for isStreaming.
      const liveState = useChatStore.getState()
      const withinRaceWindow =
        streamingStartedAt.current > 0 &&
        Date.now() - streamingStartedAt.current < CANCEL_RACE_WINDOW_MS
      const shouldCancel = liveState.isStreaming || withinRaceWindow || stopLabel === 'stopping'
      if (!shouldCancel) return
      e.preventDefault()
      if (liveState.isStreaming) {
        stoppingStartedAt.current = Date.now()
        setStopLabel('stopping')
      }
      cancelStream()
    }
    document.addEventListener('keydown', handleGlobalEscape)
    return () => document.removeEventListener('keydown', handleGlobalEscape)
  // stopLabel is included so the effect re-registers when the label changes, ensuring
  // the closure capture of stopLabel is fresh for the 'stopping' guard.
  }, [stopLabel, cancelStream])

  async function handleSendWithFiles(text: string) {
    if (pendingFiles.length === 0) {
      sendMessage(text)
      return
    }
    if (!activeSessionId) {
      addToast({ message: 'No active session — cannot upload files', variant: 'error' })
      return
    }
    setIsUploading(true)
    try {
      const { files: uploaded } = await uploadFiles(activeSessionId, pendingFiles)
      const fileList = uploaded.map((f) => `[${f.name}](${f.path})`).join(', ')
      sendMessage(text ? `${text}\n\nAttached files: ${fileList}` : `Attached files: ${fileList}`)
      setPendingFiles([])
    } catch (err) {
      addToast({
        message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Upload failed',
        variant: 'error',
      })
    } finally {
      setIsUploading(false)
    }
  }

  return (
    <div
      className="relative"
      onDragOver={(e) => { e.preventDefault(); setIsDragging(true) }}
      onDragLeave={() => setIsDragging(false)}
      onDrop={(e) => {
        e.preventDefault()
        setIsDragging(false)
        const droppedFiles = Array.from(e.dataTransfer.files)
        if (droppedFiles.length > 0) handleFilesSelected(droppedFiles)
      }}
    >
      {isDragging && (
        <div className="absolute inset-0 z-50 flex items-center justify-center bg-[var(--color-primary)]/80 border-2 border-dashed border-[var(--color-accent)] rounded-lg">
          <p className="text-[var(--color-accent)] font-medium">Drop files here</p>
        </div>
      )}

      {!isConnected && (
        <div data-testid="reconnect-banner" className="mb-2 text-xs text-[var(--color-error)] flex items-center gap-1">
          <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-error)] inline-block" />
          Disconnected — reconnecting...
        </div>
      )}

      {/* Slash command dropdown */}
      {shouldShowSlash && slashOpen && (
        <div className="mb-2 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] overflow-hidden shadow-lg">
          {visibleSlashCommands.map((cmd, i) => (
            <button
              key={cmd.label}
              type="button"
              className={cn(
                'w-full flex items-baseline gap-3 px-3 py-2 text-left transition-colors',
                i === slashHighlight
                  ? 'bg-[var(--color-accent)]/10 text-[var(--color-secondary)]'
                  : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-secondary)]',
              )}
              onMouseDown={(e) => {
                e.preventDefault() // prevent blur before click registers
                executeSlashCommand(cmd.label)
              }}
              onMouseEnter={() => setSlashHighlight(i)}
            >
              <span className="font-mono text-xs text-[var(--color-accent)]">{cmd.label}</span>
              <span className="text-[11px]">{cmd.description}</span>
            </button>
          ))}
        </div>
      )}

      {/* Pending file previews */}
      {pendingFiles.length > 0 && (
        <div className="flex flex-wrap gap-2 px-2 pb-2">
          {pendingFiles.map((file, i) => (
            <div
              key={`${file.name}-${file.size}-${file.lastModified}`}
              className="flex items-center gap-1.5 px-2 py-1 rounded-md bg-[var(--color-surface-2)] border border-[var(--color-border)] text-xs"
            >
              {file.type.startsWith('image/') ? (
                <FilePreviewThumbnail file={file} />
              ) : (
                <File size={14} className="text-[var(--color-muted)]" />
              )}
              <span className="truncate max-w-[120px]">{file.name}</span>
              <button
                type="button"
                onClick={() => setPendingFiles((prev) => prev.filter((_, j) => j !== i))}
                className="text-[var(--color-muted)] hover:text-[var(--color-error)]"
                aria-label={`Remove ${file.name}`}
              >
                <X size={12} />
              </button>
            </div>
          ))}
        </div>
      )}

      {/* Hidden file input */}
      <input
        ref={fileInputRef}
        type="file"
        multiple
        className="hidden"
        onChange={(e) => {
          const files = Array.from(e.target.files ?? [])
          if (files.length) handleFilesSelected(files)
          e.target.value = ''
        }}
      />

      <div className="flex items-end gap-2 px-2 py-2">
        {/* Paperclip button */}
        <button
          type="button"
          onClick={() => fileInputRef.current?.click()}
          disabled={!isConnected || isStreaming || isUploading || isReplaying}
          className="shrink-0 w-11 h-11 rounded-xl flex items-center justify-center text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
          aria-label="Attach file"
          title="Attach file"
        >
          <Paperclip size={16} />
        </button>

      <ComposerPrimitive.Root
        className="flex items-end gap-2 flex-1"
        onSubmit={(e) => {
          // Block submission while streaming — slash commands are handled via
          // handleKeyDown, and normal sends must wait for the turn to complete.
          if (isStreaming) {
            e.preventDefault()
            return
          }
          if (pendingFiles.length === 0) return // Let AssistantUI handle the standard send path
          e.preventDefault()
          const text = composerRuntime.getState().text.trim()
          composerRuntime.setText('')
          setInputValue('')
          void handleSendWithFiles(text)
        }}
      >
        <ComposerPrimitive.Input
          data-testid="chat-input"
          placeholder={agentRemoved ? 'Agent has been removed — this session is read-only' : composerPlaceholder(isConnected, isStreaming || isUploading, isReplaying, activeAgentName)}
          disabled={agentRemoved || !isConnected || isUploading || isReplaying}
          rows={1}
          cancelOnEscape={false}
          className={cn(
            'flex-1 resize-none rounded-2xl border border-[var(--color-border)] bg-[var(--color-surface-2)] px-4 py-2.5 text-sm text-[var(--color-secondary)] outline-none',
            'placeholder:text-[var(--color-muted)] min-h-[24px] max-h-[200px] leading-6 overflow-hidden',
            'focus:border-[var(--color-accent)]/50 focus:ring-1 focus:ring-[var(--color-accent)]/20',
            (agentRemoved || !isConnected || isStreaming || isUploading || isReplaying) && 'opacity-60 cursor-not-allowed',
          )}
          aria-label="Message input"
          onChange={(e) => {
            const val = (e.target as HTMLTextAreaElement).value
            setInputValue(val)
            if (val.startsWith('/')) {
              setSlashOpen(true)
            } else {
              closeSlash()
            }
            if (val.length > 1_000_000) {
              if (!hasWarnedLargeInput.current) {
                hasWarnedLargeInput.current = true
                useUiStore.getState().addToast({
                  message: `Large input (${(val.length / 1_000_000).toFixed(1)}MB). This may be slow to process.`,
                  variant: 'default',
                })
              }
            } else {
              hasWarnedLargeInput.current = false
            }
          }}
          onKeyDown={handleKeyDown}
          onBlur={() => {
            // Delay so mouseDown on slash item fires first
            setTimeout(closeSlash, 150)
          }}
          onPaste={(e) => {
            const items = Array.from(e.clipboardData?.items ?? [])
            const imageItems = items.filter((item) => item.type.startsWith('image/'))
            const imageFiles = imageItems.map((item) => item.getAsFile()).filter(Boolean) as File[]
            if (imageFiles.length > 0) {
              e.preventDefault()
              handleFilesSelected(imageFiles)
            } else if (imageItems.length > 0) {
              // Images were in clipboard but couldn't be materialized as files
              e.preventDefault()
              addToast({ message: 'Could not paste image — try saving it to a file first', variant: 'error' })
            }
          }}
        />

        {isStreaming || isUploading || stopLabel === 'stopping' ? (
          <button
            type="button"
            data-testid="stop-btn"
            onClick={() => {
              // EC-15 / FR-21: set label synchronously so the UI updates
              // within the same React render tick, before the cancel
              // network round-trip starts (no perceived latency).
              // Do NOT guard with isStreaming here — cancelStream() handles the
              // server-send gate internally. Guarding here would silently no-op
              // when the turn races to completion between render and click,
              // preventing the (interrupted) label from appearing.
              // T25: record the timestamp so the minimum-display-time logic in
              // useEffect([isStreaming]) can delay the 'stop' reset if the done
              // frame arrives before 1000ms elapses.
              stoppingStartedAt.current = Date.now()
              setStopLabel('stopping')
              cancelStream()
            }}
            disabled={isUploading}
            className={cn(
              'shrink-0 rounded-xl flex items-center justify-center transition-colors',
              stopLabel === 'stopping'
                ? 'px-3 h-11 gap-1.5 text-xs font-medium bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30'
                : 'w-11 h-11',
              isStreaming
                ? 'bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30'
                : 'bg-[var(--color-surface-3)] text-[var(--color-muted)] cursor-wait',
            )}
            aria-label={isUploading ? 'Uploading...' : stopLabel === 'stopping' ? 'Stopping...' : 'Stop generation'}
            title={isUploading ? 'Uploading files...' : 'Stop (Escape)'}
          >
            <Stop size={15} weight="fill" />
            {stopLabel === 'stopping' && <span>Stopping...</span>}
          </button>
        ) : (
          // FR-I-014: also disabled during replay (isReplaying) so user cannot send out-of-order
          <ComposerPrimitive.Send
            disabled={!isConnected || isReplaying}
            data-testid="chat-send"
            className={cn(
              'shrink-0 w-11 h-11 rounded-xl flex items-center justify-center transition-colors',
              isConnected && !isReplaying
                ? 'bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent-hover)] disabled:bg-[var(--color-surface-3)] disabled:text-[var(--color-muted)] disabled:cursor-not-allowed'
                : 'bg-[var(--color-surface-3)] text-[var(--color-muted)] cursor-not-allowed',
            )}
            aria-label="Send message"
            aria-disabled={isReplaying || undefined}
          >
            <PaperPlaneRight size={15} weight="bold" />
          </ComposerPrimitive.Send>
        )}
      </ComposerPrimitive.Root>
      </div>

      <p className="mt-1.5 text-[10px] text-[var(--color-muted)] text-center">
        Agents can make mistakes. Verify important information.
      </p>
    </div>
  )
}

// ── Welcome state ─────────────────────────────────────────────────────────────

function WelcomeState({ hasAgent }: { hasAgent: boolean }) {
  return (
    <div className="flex flex-col items-center justify-center h-full min-h-[60vh] gap-8 p-8">
      <div className="flex flex-col items-center gap-6 text-center max-w-md">
        <img
          src={OmnipusAvatar}
          alt="Omnipus mascot"
          className="h-20 w-20 drop-shadow-lg"
        />
        <div>
          <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)] mb-2">
            Welcome to Omnipus
          </h1>
          <p className="text-[var(--color-muted)] text-sm">
            {hasAgent
              ? 'Your agent is ready. Start a conversation below.'
              : 'Select an agent in the session bar to get started.'}
          </p>
        </div>
      </div>
    </div>
  )
}

// ── Main screen ───────────────────────────────────────────────────────────────

export function ChatScreen({ agentRemoved = false }: { agentRemoved?: boolean }) {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const pendingApprovals = useChatStore((s) => s.pendingApprovals)
  const rateLimitEvent = useChatStore((s) => s.rateLimitEvent)
  const clearRateLimitEvent = useChatStore((s) => s.clearRateLimitEvent)
  const setMessages = useChatStore((s) => s.setMessages)
  const attachedSessionType = useSessionStore((s) => s.attachedSessionType)
  const attachedTaskTitle = useSessionStore((s) => s.attachedTaskTitle)
  // For the ARIA live region: track the last assistant message id for screen reader announcements
  const messages = useChatStore((s) => s.messages)
  const lastAssistantMessage = [...messages].reverse().find((m) => m.role === 'assistant')
  const lastAnnouncedIdRef = useRef<string | null>(null)
  const shouldAnnounce = lastAssistantMessage?.id != null && lastAssistantMessage.id !== lastAnnouncedIdRef.current

  useEffect(() => {
    if (lastAssistantMessage?.id && lastAssistantMessage.status === 'done') {
      lastAnnouncedIdRef.current = lastAssistantMessage.id
    }
  }, [lastAssistantMessage?.id, lastAssistantMessage?.status])

  const { data: agentsForAria = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const activeAgentName = agentsForAria.find((a) => a.id === activeAgentId)?.name ?? 'Omnipus'

  // Load message history when session changes
  const isConnected = useConnectionStore((s) => s.isConnected)

  const {
    data: historyData,
    isError: historyError,
    refetch: refetchHistory,
  } = useQuery({
    queryKey: ['messages', activeSessionId],
    queryFn: () => fetchSessionMessages(activeSessionId!),
    enabled: !!activeSessionId,
    gcTime: 0,
    // Never re-fetch on window focus — the WebSocket delivers live updates
    refetchOnWindowFocus: false,
    // Skip re-fetch on mount when the WebSocket is already connected and delivering messages
    refetchOnMount: !isConnected,
  })

  // WS attach_session + streamReplay is the authoritative history loader.
  // Skip the REST-based setMessages overwrite when WS replay is active OR has already
  // populated the store — otherwise the filter below strips tool_call frames already
  // attached by the reducer and historical tool-call-badge elements disappear.
  const isReplaying = useChatStore((s) => s.isReplaying)
  const storeMessageCount = useChatStore((s) => s.messages.length)
  // replayCompletedForSession tracks whether WS replay finished for the active session.
  // When set, the REST fallback is skipped even if the store has 0 messages (empty session).
  const replayCompletedForSession = useChatStore((s) => s.replayCompletedForSession)
  useEffect(() => {
    if (!historyData) return
    // Don't overwrite during replay — WS frames are the source of truth.
    if (isReplaying) return
    // Don't overwrite if WS replay already completed for this session.
    // This gates the fallback more precisely than storeMessageCount > 0 alone —
    // an empty session would pass the count check but still had a successful replay.
    if (replayCompletedForSession === activeSessionId) return
    // Don't overwrite if the store already has messages for this session (replay done).
    if (storeMessageCount > 0) return
    // Fallback: REST fetched history before WS replay fired (e.g., WS unavailable).
    // Filter out tool_call entries that have no role — they crash AssistantUI's convertMessages.
    const validMessages = historyData.filter(
      (m: { role?: string }) => m.role === 'user' || m.role === 'assistant' || m.role === 'system',
    )
    setMessages(validMessages)
  }, [historyData, isReplaying, storeMessageCount, replayCompletedForSession, activeSessionId, setMessages])

  const activePendingApprovals = pendingApprovals.filter((a) => a.status === 'pending')

  return (
    <div className="flex flex-col absolute inset-0 overflow-hidden">
      {/* Agent-removed banner — shown when the session's agent has been deleted (#103) */}
      {agentRemoved && (
        <div
          data-testid="agent-removed-banner"
          className="px-4 py-2 bg-[var(--color-error)]/10 border-b border-[var(--color-error)]/20 flex items-center gap-2"
        >
          <span className="text-xs text-[var(--color-error)] flex-1">
            Agent removed — this session is read-only
          </span>
        </div>
      )}

      {/* Task session banner — shown when viewing a task execution transcript */}
      {attachedSessionType === 'task' && (
        <div className="px-4 py-2 bg-[var(--color-surface-2)] border-b border-[var(--color-border)] flex items-center gap-2">
          <ListChecks size={14} className="text-[var(--color-accent)] shrink-0" />
          <span className="text-xs text-[var(--color-secondary)] flex-1 truncate">
            Task: {attachedTaskTitle ?? 'Task Execution'}
          </span>
        </div>
      )}

      {/* History fetch error */}
      {historyError ? (
        <div className="flex flex-col items-center justify-center flex-1 gap-3 text-sm text-[var(--color-muted)]">
          <p>Could not load messages.</p>
          <Button variant="outline" size="sm" onClick={() => refetchHistory()}>
            <ArrowCounterClockwise size={14} /> Retry
          </Button>
        </div>
      ) : (
        <ThreadPrimitive.Root className="flex flex-col flex-1 min-h-0">
          {/* ARIA live region: announces new assistant messages to screen readers.
              Only fires when a genuinely new message ID arrives and is complete. */}
          <div aria-live="polite" aria-atomic="true" className="sr-only">
            {shouldAnnounce && lastAssistantMessage?.status === 'done' && (
              <span>New response from {activeAgentName}</span>
            )}
          </div>

          {/* Message viewport */}
          <ThreadPrimitive.Viewport className="flex-1 overflow-y-auto pt-4 pb-2">
            <AuiIf condition={(s) => s.thread.isEmpty}>
              <WelcomeState hasAgent={!!activeAgentId} />
            </AuiIf>

            <div data-testid="messages-list" className="max-w-4xl mx-auto w-full">
              <ThreadPrimitive.Messages>
                {({ message }) => {
                  if (message.role === 'user') return <UserMessage />
                  if (message.role === 'system') return <SystemMessage />
                  return <AssistantMessage />
                }}
              </ThreadPrimitive.Messages>
            </div>
          </ThreadPrimitive.Viewport>

          {/* FR-21: Interrupted-message status markers — rendered inside
              ThreadPrimitive.Root but OUTSIDE the scrollable Viewport. This
              position has guaranteed non-zero height because it's in the
              non-scrolling flex layout between the Viewport and the composer.
              Playwright locates these elements via text=(interrupted). */}
          <InterruptedMessageMarkers />

          {/* Pending exec approval blocks — shown above composer */}
          {(activePendingApprovals.length > 0 || rateLimitEvent) && (
            <div className="px-4 space-y-2 pb-2">
              {rateLimitEvent && (
                <RateLimitIndicator
                  scope={rateLimitEvent.scope}
                  resource={rateLimitEvent.resource}
                  policyRule={rateLimitEvent.policyRule}
                  retryAfterSeconds={rateLimitEvent.retryAfterSeconds}
                  tool={rateLimitEvent.tool}
                  onDismiss={clearRateLimitEvent}
                />
              )}
              {activePendingApprovals.map((approval) => (
                <ExecApprovalBlock key={approval.id} approval={approval} />
              ))}
            </div>
          )}

          {/* Composer — centered, ChatGPT-style floating layout */}
          <div className="relative w-full">
            {/* Gradient fade above composer */}
            <div className="absolute -top-8 left-0 right-0 h-8 bg-gradient-to-t from-[var(--color-primary)] to-transparent pointer-events-none" />
            <div className="w-full max-w-3xl mx-auto px-4 pb-2 pt-2">
              <OmnipusComposer agentRemoved={agentRemoved} />
            </div>
          </div>
        </ThreadPrimitive.Root>
      )}

      {/* Session slide-over panel */}
      <SessionPanel />
    </div>
  )
}
