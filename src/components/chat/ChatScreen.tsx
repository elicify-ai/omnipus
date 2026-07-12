import React, { useEffect, useRef, useState, useCallback } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useQuery } from '@tanstack/react-query'
import {
  ThreadPrimitive,
  MessagePrimitive,
  ComposerPrimitive,
  AttachmentPrimitive,
  useAttachment,
  ActionBarPrimitive,
  AuiIf,
  useComposerRuntime,
  useMessage,
  useThreadViewportStore,
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
  WifiSlash,
  ArrowClockwise,
  Clock,
  Lightning,
} from '@phosphor-icons/react'
import OmnipusAvatar from '@/assets/logo/omnipus-avatar.svg?url'
import { IconRenderer } from '@/components/shared/IconRenderer'
import { SessionPanel } from './SessionPanel'
import { GenericToolCall } from './tools/GenericToolCall'
import { WebServeBlock } from './tools/WebServeUI'
import { BrowserToolReplayBlock, isReplayBrowserToolName } from './tools/BrowserTool'
import { RateLimitIndicator } from './RateLimitIndicator'
import { ActivityBar } from './ActivityBar'
import { MarkdownText } from './markdown-text'
import { SubagentBlock } from './SubagentBlock'
import { ModelFooter } from './ModelFooter'
import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { useChatStore } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { fetchAgents, fetchSessionMessages, fetchCommands, fetchSkills } from '@/lib/api'
import type { SlashCommand, Skill } from '@/lib/api'
import { AttachmentCard, AttachmentRemoveX, useFilePreview } from './AttachmentCard'
import { cn } from '@/lib/utils'
import { HistoricalMessageMarkdown } from './historical-markdown'
import { ChatImage } from './ChatImage'
import { useSlashMenu } from '@/hooks/useSlashMenu'
import { useFileUpload } from '@/hooks/useFileUpload'
import { useCancelState } from '@/hooks/useCancelState'

// ── Skill-aware message content renderer (R2/F1/F7/F9) ───────────────────────

// renderSkillAwareContent — shared helper used by both the live AssistantUI
// UserMessage (which has MessagePrimitive.Parts) and VirtualUserMessageRow
// (historical/reloaded messages). Keeps the chip logic in one place so that
// reloaded messages display identically to live ones (F1).
// Exported for focused unit tests (F1 test requirement).
//
// F7: a leading /token that matches a known built-in command label (e.g.
// /clear, /help, /model, /agents) must NOT produce a chip — built-ins win (D3).
// We receive the `commands` list and use it to exclude those tokens.
//
// F9: the regex uses [\s\S]* so multi-line bodies (foo\nbar) are captured.
//
// Returns the JSX for the inner message bubble content; the caller wraps it in
// the appropriate outer container (MessagePrimitive.Root or a plain div).
export function renderSkillAwareContent(
  content: string | null,
  skills: Skill[],
  // Command label strings, e.g. ['/clear', '/help', '/model'] — used to apply
  // the F7 built-in precedence gate. Pass the fetched commands list when
  // available; callers that can't access the store pass [].
  commandLabels: string[],
  // Fallback plain-text renderer — used by live UserMessage (which has access
  // to MessagePrimitive.Parts) vs historical messages (which use a simple <p>).
  fallbackRenderer: () => React.ReactNode,
): React.ReactNode {
  // F9: [\s\S]* captures newlines; without the s/dotAll flag this was broken
  // for multi-line messages.
  const skillMatch = content?.match(/^\/(\S+)(?:\s+([\s\S]*))?$/)

  // F7: skip chip when the leading /token is a known built-in command.
  const isBuiltin = skillMatch
    ? commandLabels.some((lbl) => lbl.toLowerCase() === `/${skillMatch[1].toLowerCase()}`)
    : false

  const matchedSkill =
    skillMatch && !isBuiltin
      ? skills.find((s) => s.id.toLowerCase() === skillMatch[1].toLowerCase())
      : null

  if (matchedSkill && skillMatch) {
    // R2: compact skill indicator — show message text + chip, or chip alone
    return (
      <div className="rounded-xl px-4 py-3 text-sm leading-relaxed bg-[var(--color-surface-2)] text-[var(--color-secondary)] rounded-tr-sm flex flex-col gap-1">
        {skillMatch[2] ? (
          <>
            <p className="whitespace-pre-wrap break-words">{skillMatch[2]}</p>
            <span className="flex items-center gap-1 text-[10px] text-[var(--color-muted)]">
              <Lightning size={10} weight="fill" className="text-[var(--color-accent)]" />
              skill: {matchedSkill.name}
            </span>
          </>
        ) : (
          <span className="flex items-center gap-1 text-xs">
            <Lightning size={12} weight="fill" className="text-[var(--color-accent)]" />
            {matchedSkill.name}
          </span>
        )}
      </div>
    )
  }

  return fallbackRenderer()
}

// ── Message components ────────────────────────────────────────────────────────

function UserMessage() {
  const message = useMessage()
  const { data: skills = [] } = useQuery<Skill[]>({
    queryKey: ['skills'],
    queryFn: () => fetchSkills(),
    staleTime: 60_000,
  })
  const { data: commands = [] } = useQuery<SlashCommand[]>({
    queryKey: ['commands', 'web'],
    queryFn: () => fetchCommands('web'),
    staleTime: 60_000,
  })

  const content = (() => {
    const parts = message.content
    if (!parts || parts.length === 0) return null
    const textPart = parts.find((p: { type: string }) => p.type === 'text')
    if (!textPart || typeof (textPart as { text?: string }).text !== 'string') return null
    return (textPart as { text: string }).text
  })()

  const commandLabels = commands.map((c) => c.label)

  return (
    <MessagePrimitive.Root data-testid="user-message" data-message-id={message.id} className="group flex gap-3 px-4 py-3 flex-row-reverse">
      <div className="shrink-0 w-7 h-7 rounded-full flex items-center justify-center bg-[var(--color-accent)]/20 text-[var(--color-accent)]">
        <User size={14} weight="bold" />
      </div>
      <div className="flex flex-col items-end gap-1 max-w-[85%] min-w-0">
        {renderSkillAwareContent(content, skills, commandLabels, () => (
          <div className="rounded-xl px-4 py-3 text-sm leading-relaxed bg-[var(--color-surface-2)] text-[var(--color-secondary)] rounded-tr-sm">
            <MessagePrimitive.Parts>
              {({ part }) => {
                if (part.type !== 'text') return null
                return <p className="whitespace-pre-wrap break-words">{part.text}</p>
              }}
            </MessagePrimitive.Parts>
          </div>
        ))}
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
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const liveCall = storeToolCalls[props.toolCallId]
  return (
    <GenericToolCall
      toolName={props.toolName}
      args={props.args}
      result={liveCall?.result ?? props.result}
      status={props.status}
      error={liveCall?.error}
      durationMs={liveCall?.duration_ms}
      sessionId={activeSessionId ?? ''}
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
          <div key={`${m.url}-${i}`} className="max-w-2xl">
            <ChatImage
              src={m.url}
              alt={m.caption || m.filename}
              filename={m.filename}
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

// ── Standalone message row components (virtualizer) ──────────────────────────
// Render ChatMessage from props (no AssistantUI context) for use by the virtualizer.

/** Inline retry button for user messages that failed to send (#253b). */
function UserMessageRetryButton({ message }: { message: ChatMessage }) {
  const sendMessage = useChatStore((s) => s.sendMessage)
  const isStreaming = useChatStore((s) => s.isStreaming)

  if (message.status !== 'error' || isStreaming) return null

  function handleRetry() {
    // #253(c): resend the original user message content.
    sendMessage(message.content)
  }

  return (
    <div className="flex items-center justify-end gap-2 mt-1">
      <span className="text-[10px] text-[var(--color-error)]">Send failed</span>
      <button
        type="button"
        data-testid="user-message-retry"
        onClick={handleRetry}
        aria-label="Retry — resend this message"
        className="flex items-center gap-1 px-2 py-1 rounded text-[10px] text-[var(--color-error)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
        title="Retry — resend this message"
      >
        <ArrowCounterClockwise size={11} />
        <span>Retry</span>
      </button>
    </div>
  )
}

/** Standalone user message row for the virtualizer. */
// useSkillChipData fetches the skills + web commands ONCE per message list (not
// per row) for the R2 skill-chip detection. Cache hits are free (staleTime 60s).
function useSkillChipData(): { skills: Skill[]; commandLabels: string[] } {
  const { data: skills = [] } = useQuery<Skill[]>({
    queryKey: ['skills'],
    queryFn: () => fetchSkills(),
    staleTime: 60_000,
  })
  const { data: commands = [] } = useQuery<SlashCommand[]>({
    queryKey: ['commands', 'web'],
    queryFn: () => fetchCommands('web'),
    staleTime: 60_000,
  })
  return { skills, commandLabels: commands.map((c) => c.label) }
}

// F1/R2: skills + commandLabels are passed in by the parent list (fetched ONCE
// per list, not per row) so a 1000-row virtualized transcript doesn't spawn a
// query subscription per row. The shared renderSkillAwareContent makes reloaded
// messages show the skill chip identically to live messages (FR-013).
// Exported for focused unit tests (F1 test requirement — reload path).
export function VirtualUserMessageRow({
  message,
  skills,
  commandLabels,
}: {
  message: ChatMessage
  skills: Skill[]
  commandLabels: string[]
}) {
  const isError = message.status === 'error'

  return (
    <div
      data-testid="user-message"
      data-message-role="user"
      data-message-id={message.id}
      data-status={message.status}
      className="group flex gap-3 px-4 py-3 flex-row-reverse"
    >
      <div className="shrink-0 w-7 h-7 rounded-full flex items-center justify-center bg-[var(--color-accent)]/20 text-[var(--color-accent)]">
        <User size={14} weight="bold" />
      </div>
      <div className="flex flex-col items-end gap-1.5 max-w-[85%] min-w-0">
        {/* Attachments the user sent — image thumbnails + colour-coded file
            cards, shown above the text like ChatGPT. */}
        {message.media && message.media.length > 0 && (
          <div className="flex flex-wrap gap-2 justify-end">
            {message.media.map((m, i) =>
              m.type === 'image' ? (
                // Sent images (screenshots) get the full ChatImage treatment — hover
                // toolbar (copy/share/download/enlarge) + click-to-enlarge lightbox.
                <ChatImage key={`${m.url}-${i}`} src={m.url} alt={m.filename} filename={m.filename} className="max-w-[240px]" />
              ) : (
                <AttachmentCard key={`${m.url}-${i}`} filename={m.filename} contentType={m.contentType} />
              ),
            )}
          </div>
        )}
        {message.content.trim().length > 0 && (
          renderSkillAwareContent(
            message.content,
            skills,
            commandLabels,
            () => (
              <div className={cn(
                "rounded-xl px-4 py-3 text-sm leading-relaxed rounded-tr-sm",
                isError
                  ? "bg-[var(--color-error)]/10 border border-[var(--color-error)]/30 text-[var(--color-secondary)]"
                  : "bg-[var(--color-surface-2)] text-[var(--color-secondary)]"
              )}>
                <p className="whitespace-pre-wrap break-words">{message.content}</p>
              </div>
            ),
          )
        )}
        {/* #253(b): show error + Retry when message failed to send */}
        <UserMessageRetryButton message={message} />
      </div>
    </div>
  )
}

/** Standalone system message row for the virtualizer. */
function VirtualSystemMessageRow({ message }: { message: ChatMessage }) {
  return (
    <div
      data-message-role="system"
      data-message-id={message.id}
      className="flex justify-center py-2"
    >
      <div className="text-xs text-[var(--color-muted)] bg-[var(--color-surface-2)] px-3 py-1 rounded-full">
        <span>{message.content}</span>
      </div>
    </div>
  )
}

/** Copy-to-clipboard helper for static assistant messages. */
function StaticCopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  const handleCopy = useCallback(() => {
    void navigator.clipboard.writeText(text).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }, [text])
  return (
    <button
      type="button"
      aria-label="Copy message"
      onClick={handleCopy}
      className="flex items-center gap-1 px-2 py-1 rounded text-[10px] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
      title="Copy message"
    >
      {copied ? (
        <>
          <Check size={11} weight="bold" className="text-[var(--color-success)]" />
          <span>Copied!</span>
        </>
      ) : (
        <>
          <Copy size={11} />
          <span>Copy</span>
        </>
      )}
    </button>
  )
}

/** Standalone assistant message row for the virtualizer (historical / completed messages). */
function VirtualAssistantMessageRow({ message, liteMode }: { message: ChatMessage; liteMode: boolean }) {
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const toolCalls = useChatStore((s) => s.toolCalls)

  const messageAgentId = message.agentId ?? activeAgentId
  const agent = agents.find((a) => a.id === messageAgentId)
  const agentDisplayName = agent?.name ?? (messageAgentId || null)
  const isInterrupted = message.status === 'interrupted'

  // Render media attachments.
  const mediaItems = message.media ?? []

  // Build tool-call parts from the message's stored tool_calls list.
  // In lite mode, tool calls start collapsed (expanded=false by default in GenericToolCall).
  const storeToolCallIds = (message.tool_calls ?? []).map((tc) => tc.id)

  // D-fix: this row is also used by PlainMessageList (the ResizeObserver-
  // unavailable fallback), which — unlike VirtualizedMessageListInner — does
  // NOT split the live streaming placeholder out into ThreadPrimitive.Messages;
  // it renders every message, including an in-flight one, through this
  // component. Without this guard, a message with isStreaming:true and empty
  // content would show the same "avatar + Copy, nothing to copy" broken bubble
  // as the live path (see AssistantMessage's showEmptyPlaceholder).
  const hasContent = !!message.content?.trim().length
  const hasToolCalls = storeToolCallIds.length > 0
  const hasMedia = mediaItems.length > 0
  const hasSpans = (message.spans ?? []).length > 0
  const showEmptyPlaceholder = !!message.isStreaming && !hasContent && !hasToolCalls && !hasMedia && !hasSpans

  return (
    <div
      data-testid="assistant-message"
      data-message-role="assistant"
      data-message-id={message.id}
      data-status="complete"
      className="group flex gap-3 px-4 py-3"
    >
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
      <div className="flex flex-col gap-1 max-w-[85%] min-w-0 flex-1">
        {agentDisplayName && (
          <span data-testid="agent-label" className="text-[10px] text-[var(--color-muted)]">
            {agentDisplayName}
          </span>
        )}
        <div className="text-sm leading-relaxed text-[var(--color-secondary)]">
          {showEmptyPlaceholder && <ThinkingIndicator />}
          {/* Media attachments */}
          {!showEmptyPlaceholder && mediaItems.length > 0 && (
            <div className="flex flex-col gap-2 mb-2">
              {mediaItems.map((m, i) =>
                m.type === 'image' ? (
                  <div key={`${m.url}-${i}`} className="max-w-2xl">
                    <ChatImage
                      src={m.url}
                      alt={m.caption || m.filename}
                      filename={m.filename}
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
          )}

          {/* Text content — use react-markdown for static historical messages.
              The live streaming message uses the full AssistantUI MarkdownText
              (with Shiki highlighting, Mermaid, etc.) via ThreadPrimitive.Messages. */}
          {message.content && <HistoricalMessageMarkdown content={message.content} />}

          {/* Tool calls — rendered from stored tool_calls list */}
          {storeToolCallIds.map((callId) => {
            const tc = toolCalls[callId] ?? (message.tool_calls ?? []).find((t) => t.id === callId)
            if (!tc) return null
            // Parity with the live AssistantUI dispatch in OmnipusRuntimeProvider:
            // web_serve / serve_workspace / run_in_workspace go through WebServeBlock
            // here too, so replayed sessions render the iframe (or the malformed
            // result block) instead of a collapsed generic badge.
            if (tc.tool === 'serve_workspace' || tc.tool === 'run_in_workspace' || tc.tool === 'web_serve') {
              return (
                <WebServeBlock
                  key={callId}
                  args={(tc.params ?? {}) as { path?: string; command?: string; port?: number; duration_seconds?: number }}
                  result={tc.result ?? null}
                  isRunning={false}
                  toolName={tc.tool}
                />
              )
            }
            // B-fix: the six browser.*/browser_* tools also have a registered
            // live UI (BrowserToolBlock, dispatched via makeAssistantToolUI in
            // OmnipusRuntimeProvider) that parses the tool's result into a
            // screenshot/text/error card instead of showing raw JSON. Route
            // replay through the same block for live/replay parity — see
            // BrowserToolReplayBlock's doc comment for the full story.
            if (isReplayBrowserToolName(tc.tool)) {
              return (
                <BrowserToolReplayBlock
                  key={callId}
                  toolName={tc.tool}
                  args={tc.params}
                  result={tc.result}
                  status={{ type: 'complete' }}
                />
              )
            }
            return (
              <GenericToolCall
                key={callId}
                toolName={tc.tool}
                args={tc.params}
                result={tc.result}
                status={{ type: 'complete' }}
                error={tc.error}
                durationMs={tc.duration_ms}
                defaultCollapsed={liteMode}
                sessionId={activeSessionId ?? ''}
              />
            )
          })}

          {/* Subagent spans */}
          {(message.spans ?? []).map((span) => (
            <SubagentBlock key={span.spanId} span={span} />
          ))}
        </div>

        {/* Action bar — always visible at reduced opacity, fully opaque on hover.
            Suppressed while showEmptyPlaceholder (D-fix): nothing has streamed
            in yet, so there is nothing to copy. */}
        {!showEmptyPlaceholder && (
          <div className="flex items-center gap-1 opacity-70 hover:opacity-100 transition-opacity duration-150">
            <StaticCopyButton text={message.content ?? ''} />
          </div>
        )}

        {/* Interrupted label */}
        {isInterrupted && (
          <span className="text-[10px] text-[var(--color-muted)] italic px-1">(interrupted)</span>
        )}

        {/* Per-turn model record (FR-014). Mirrors MessageItem.tsx so
            replay sessions show the same model footer as the live
            AssistantUI render. */}
        {message.role === 'assistant' && <ModelFooter model={message.model} />}
      </div>
    </div>
  )
}

/** Plain (non-virtualized) message list — fallback when ResizeObserver is unavailable. */
function PlainMessageList({ messages, liteMode }: { messages: ChatMessage[]; liteMode: boolean }) {
  const { skills, commandLabels } = useSkillChipData()
  return (
    <div
      data-testid="virtualized-message-list"
      className="flex-1 overflow-y-auto overscroll-contain pt-4 pb-2"
    >
      <div className="max-w-4xl mx-auto w-full">
        {messages.map((msg) => {
          if (msg.role === 'user')
            return (
              <VirtualUserMessageRow
                key={msg.id}
                message={msg}
                skills={skills}
                commandLabels={commandLabels}
              />
            )
          if (msg.role === 'system') return <VirtualSystemMessageRow key={msg.id} message={msg} />
          return <VirtualAssistantMessageRow key={msg.id} message={msg} liteMode={liteMode} />
        })}
      </div>
    </div>
  )
}

let _resizeObserverWarnEmitted = false

/**
 * Virtualized historical message list (AssistantUI constraint: the live
 * streaming message stays in ThreadPrimitive.Messages below for full context).
 */
function VirtualizedMessageList({
  messages,
  liteMode,
}: {
  messages: ChatMessage[]
  liteMode: boolean
}) {
  // Feature-detect ResizeObserver at render time (not module load) so test
  // environments that stub it in beforeEach are detected correctly.
  // iOS < 13.4 and some enterprise Android WebViews lack it — use the plain
  // fallback to avoid a white-screen crash.
  if (typeof ResizeObserver === 'undefined') {
    if (!_resizeObserverWarnEmitted) {
      _resizeObserverWarnEmitted = true
      console.warn('[chat] ResizeObserver unavailable — rendering full message list without virtualization')
    }
    return <PlainMessageList messages={messages} liteMode={liteMode} />
  }
  return <VirtualizedMessageListInner messages={messages} liteMode={liteMode} />
}

/**
 * Re-pins the scroll container to the bottom when the VIRTUALIZER grows the
 * transcript via the spacer's inline `style` height — row measurement, the
 * live→virtualized finalize hand-off, and late-rendering mermaid/images. The
 * assistant-ui Viewport autoscroll observes content via a subtree MutationObserver
 * that deliberately ignores style-only mutations (so the virtualizer's translateY
 * can't cause a feedback loop), which means it never sees that height growth and
 * the tail of a message can sit below the fold — most visibly on slower devices
 * (iPad) and with diagrams that render after the stream completes. A ResizeObserver
 * on the content fires on the height growth (and NOT on transforms, which don't
 * change size), so we re-pin — but only when the engine still reports we're at the
 * bottom (reused via the Viewport store), so a user who scrolled up to read is
 * never yanked down. MUST render inside ThreadPrimitive.Viewport to read its store.
 */
function VirtualizerAutoFollow({
  scrollRef,
  contentRef,
}: {
  scrollRef: React.RefObject<HTMLDivElement | null>
  contentRef: React.RefObject<HTMLDivElement | null>
}) {
  const viewportStore = useThreadViewportStore()
  useEffect(() => {
    const el = scrollRef.current
    const content = contentRef.current
    if (!el || !content) return undefined
    let raf = 0
    const ro = new ResizeObserver(() => {
      // Only follow if the engine still considers us at the bottom — this reuses
      // its scrollTop-direction "did the user scroll up" detection, so we never
      // fight a user reading history. isAtBottom is unchanged by a content-growth
      // resize (the engine updates it on scroll events, not on resize), so it
      // still reflects the pre-growth position here.
      if (!viewportStore.getState().isAtBottom) return
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => {
        el.scrollTop = el.scrollHeight
      })
    })
    ro.observe(content)
    return () => {
      cancelAnimationFrame(raf)
      ro.disconnect()
    }
  }, [viewportStore, scrollRef, contentRef])
  return null
}

function VirtualizedMessageListInner({
  messages,
  liteMode,
}: {
  messages: ChatMessage[]
  liteMode: boolean
}) {
  const isStreaming = useChatStore((s) => s.isStreaming)
  const scrollContainerRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  const { skills, commandLabels } = useSkillChipData()

  // Separate the live streaming message from completed history.
  const hasStreamingMessage = isStreaming && messages.length > 0 && messages[messages.length - 1]?.isStreaming
  const historicalMessages = hasStreamingMessage ? messages.slice(0, messages.length - 1) : messages

  const virtualizer = useVirtualizer({
    count: historicalMessages.length,
    getScrollElement: () => scrollContainerRef.current,
    estimateSize: () => 80,
    overscan: 5,
    // measureElement default (getBoundingClientRect().height) is correct; no override needed.
  })

  const virtualItems = virtualizer.getVirtualItems()

  const rowForMessage = (msg: ChatMessage) => {
    if (msg.role === 'user')
      return <VirtualUserMessageRow message={msg} skills={skills} commandLabels={commandLabels} />
    if (msg.role === 'system') return <VirtualSystemMessageRow message={msg} />
    return <VirtualAssistantMessageRow message={msg} liteMode={liteMode} />
  }

  // Stick-to-bottom is delegated to assistant-ui's Viewport engine
  // (useThreadViewportAutoScroll + useOnResizeContent, via ThreadPrimitive
  // .Viewport). It attaches a ResizeObserver + a subtree MutationObserver to THIS
  // scroll container, so it follows ALL content growth — streamed tokens,
  // tool-call blocks, inline images/mermaid that render late, our virtualizer's
  // row add/remove, AND the live→virtualized finalize hand-off — and scrolls to
  // the bottom only while the user is already at the bottom. isAtBottom is keyed
  // off real scrollTop movement (not a touch-gesture heuristic), so a user
  // scroll-up correctly suspends auto-follow on touch/momentum — the failure mode
  // of the previous hand-rolled engine. We keep our custom virtualizer for row
  // rendering and only let Viewport own the scroll element + autoscroll. The
  // Viewport self-provides its viewport context; it runs within the chat's
  // AssistantRuntimeProvider + ThreadPrimitive.Root (the live ThreadPrimitive
  // .Messages below needs the runtime). The parent feature-detects ResizeObserver
  // and falls back to PlainMessageList where it is unavailable.
  //
  // VirtualizerAutoFollow (rendered inside the Viewport) covers the one growth the
  // engine can't see: the virtualizer grows the transcript by setting the spacer's
  // inline `style` height (row measurement, the live→virtualized finalize, and
  // late mermaid/image render), and the engine's MutationObserver deliberately
  // ignores style-only mutations (to avoid a translateY feedback loop). Without
  // this, the tail of a finalized/diagram message sits below the fold on slower
  // devices (iPad). It re-pins only when the engine still reports isAtBottom.
  return (
    <ThreadPrimitive.Viewport
      ref={scrollContainerRef}
      autoScroll
      data-testid="virtualized-message-list"
      className="flex-1 overflow-y-auto overscroll-contain pt-4 pb-2"
      style={{ position: 'relative' }}
    >
      <VirtualizerAutoFollow scrollRef={scrollContainerRef} contentRef={contentRef} />
      <div ref={contentRef} className="max-w-4xl mx-auto w-full">
        <div
          style={{
            height: `${virtualizer.getTotalSize()}px`,
            position: 'relative',
          }}
        >
          <div
            style={{
              position: 'absolute',
              top: 0,
              left: 0,
              width: '100%',
              transform: `translateY(${virtualItems[0]?.start ?? 0}px)`,
            }}
          >
            {virtualItems.map((virtualRow) => {
              const msg = historicalMessages[virtualRow.index]
              if (!msg) return null
              return (
                <div
                  key={virtualRow.key}
                  data-index={virtualRow.index}
                  ref={virtualizer.measureElement}
                >
                  {rowForMessage(msg)}
                </div>
              )
            })}
          </div>
        </div>

        {/* Live streaming message — kept in ThreadPrimitive.Messages for full
            AssistantUI context (streaming primitives, registered tool UIs). */}
        {hasStreamingMessage && (
          <div data-testid="streaming-message-anchor">
            <ThreadPrimitive.Messages>
              {({ message }) => {
                const isLast = message.id === messages[messages.length - 1]?.id
                if (!isLast) return null
                if (message.role === 'user') return <UserMessage />
                if (message.role === 'system') return <SystemMessage />
                return <AssistantMessage />
              }}
            </ThreadPrimitive.Messages>
          </div>
        )}
      </div>
    </ThreadPrimitive.Viewport>
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

  // D-fix: the chat store's optimistic assistant placeholder starts as
  // content:'' / status:'streaming' the instant a message is sent (store/chat.ts
  // sendMessage), so this component mounts and renders for the window before the
  // first token/tool-call/media/span arrives. Unconditionally rendering the Copy
  // (+ Retry) action bar in that window shows a "Copy" affordance for literally
  // nothing to copy — two live UAT testers flagged the resulting empty-bubble-
  // with-Copy-button as looking broken. While running with nothing to show yet,
  // render only the thinking indicator and suppress the action bar; both return
  // the moment any real content lands (text, tool call, media, or subagent span).
  const isRunning = message.status?.type === 'running'
  const hasVisibleText = message.content?.some(
    (part) => part.type === 'text' && part.text.trim().length > 0,
  )
  const hasToolCall = message.content?.some((part) => part.type === 'tool-call')
  const hasMedia = !!storeMsg?.media?.length
  const hasSpans = !!storeMsg?.spans?.length
  const showEmptyPlaceholder = isRunning && !hasVisibleText && !hasToolCall && !hasMedia && !hasSpans

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
          {showEmptyPlaceholder ? (
            // Nothing has streamed in yet — show only the thinking indicator,
            // not an empty text bubble + Copy affordance (D-fix).
            <InlineThinkingIndicator />
          ) : (
            <>
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
            </>
          )}
        </div>

        {/* Action bar — Copy + Retry buttons, always visible at reduced opacity.
            Suppressed while showEmptyPlaceholder (D-fix): nothing has streamed
            in yet, so there is nothing to copy or retry. */}
        {!showEmptyPlaceholder && (
          <ActionBarPrimitive.Root className="flex items-center gap-1 opacity-70 hover:opacity-100 transition-opacity duration-150">
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
        )}
        {/* FR-21: interrupted status label — shown when the turn was cancelled */}
        {isInterrupted && (
          <span className="text-[10px] text-[var(--color-muted)] italic px-1">(interrupted)</span>
        )}
      </div>
    </MessagePrimitive.Root>
  )
}

// ── Composer ──────────────────────────────────────────────────────────────────

function composerPlaceholder(
  canSendOrQueue: boolean,
  isStreaming: boolean,
  isReplaying: boolean,
  agentName: string,
  gaveUp: boolean,
): string {
  if (gaveUp) return 'Connection lost — click Reconnect now above'
  if (!canSendOrQueue) return 'Connecting to gateway...'
  if (isReplaying) return 'Loading session history...'
  if (isStreaming) return 'Waiting for response...'
  return `Message ${agentName}…`
}

/**
 * Renders one pending composer attachment as a ChatGPT-style card — an image
 * preview (from the local File) or a colour-coded, type-specific file card —
 * via the native useAttachment() context that ComposerPrimitive.Attachments
 * provides. Removal uses the native AttachmentPrimitive.Remove.
 */
function ComposerAttachmentChip() {
  const attachment = useAttachment()
  const file = attachment.file
  const contentType = attachment.contentType ?? file?.type
  const imageUrl = useFilePreview(file, contentType)

  return (
    <AttachmentCard
      filename={attachment.name}
      contentType={contentType}
      imageUrl={imageUrl}
      className={cn('group', imageUrl && 'w-16 h-16')}
      removeButton={
        <AttachmentPrimitive.Remove
          className="absolute -top-1.5 -right-1.5 opacity-0 group-hover:opacity-100 focus:opacity-100 [@media(hover:none)]:opacity-100 transition-opacity"
          aria-label={`Remove ${attachment.name}`}
        >
          <AttachmentRemoveX />
        </AttachmentPrimitive.Remove>
      }
    />
  )
}

// Exported for unit testing (TDD row 22).
export function OmnipusComposer({ agentRemoved = false }: { agentRemoved?: boolean }) {
  const isStreaming = useChatStore((s) => s.isStreaming)
  // FR-I-014: disable send while replay frames are still arriving
  const isReplaying = useChatStore((s) => s.isReplaying)
  const isConnected = useConnectionStore((s) => s.isConnected)
  const reconnectPhase = useConnectionStore((s) => s.reconnectPhase)
  const reconnectAttempt = useConnectionStore((s) => s.reconnectAttempt)
  const reconnect = useConnectionStore((s) => s.reconnect)
  const outboundQueue = useChatStore((s) => s.outboundQueue)
  // BUG FIX (2026-07): messages moved out of outboundQueue by drainOutboundQueue
  // are sent one at a time (see maybeDrainNext in store/chat.ts) rather than all
  // at once, so a still-draining batch must keep counting here — otherwise this
  // banner would drop to 0 the instant reconnect fires even though several
  // messages are still waiting for their turn to go out.
  const pendingDrainQueue = useChatStore((s) => s.pendingDrainQueue)
  const cancelStream = useChatStore((s) => s.cancelStream)
  const appendMessage = useChatStore((s) => s.appendMessage)
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const startNewSession = useSessionStore((s) => s.startNewSession)
  const composerRuntime = useComposerRuntime()

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const activeAgentName = agents.find((a) => a.id === activeAgentId)?.name ?? 'Omnipus'

  // Tracks whether we already warned for the current large-input threshold crossing,
  // so we only fire one toast per paste/input event that exceeds 1MB.
  const hasWarnedLargeInput = useRef(false)

  // Input is enabled unless: agent removed, replaying, uploading, or gave up on reconnect.
  // While reconnecting (fast or slow phase), input stays enabled — messages go to the queue.
  // When the WS drops (network offline, gateway restart), the textarea must
  // also disable, not just the Send button. Letting the user type into an input
  // that can't dispatch is misleading; the "Connection lost" banner alone is
  // easy to miss when the textarea looks fully interactive.
  const inputEnabled = !agentRemoved && !isReplaying && !(reconnectPhase === 'gave_up') && isConnected

  // The 3 previously-tangled composer concerns (slash/skill palette, file
  // upload incl. harmful-file confirm, stop/cancel state machine) each own
  // their own state via a dedicated hook — see src/hooks/useSlashMenu.ts,
  // useFileUpload.ts, useCancelState.ts. cancelState is instantiated first
  // because the slash menu's `/cancel` command delegates to it.
  const cancelState = useCancelState(isStreaming, cancelStream)
  const fileUpload = useFileUpload(composerRuntime)
  const slashMenu = useSlashMenu({
    isStreaming,
    isReplaying,
    inputEnabled,
    composerRuntime,
    appendMessage,
    startNewSession,
    cancelIfStreaming: cancelState.cancelIfStreaming,
  })

  // Top-level keydown orchestration — precedence matches the original
  // composer exactly: cancel-Escape first (US-1.4/FR-23), then
  // Enter-blocked-while-streaming, then slash-menu navigation.
  function handleKeyDown(e: React.KeyboardEvent) {
    // US-1.4 / FR-23: Escape cancels a turn.
    // Only morph the button to "Stopping..." if the turn is actively streaming
    // (same logic as the /cancel command and the stop button click). Pressing
    // Escape on a completed turn still marks the message as interrupted via
    // cancelStream() → markLastMessageInterrupted(), but there is no streaming
    // button to morph — setting 'stopping' when isStreaming is false would leave
    // the button stuck because the reset effect does not fire again.
    if (e.key === 'Escape' && (isStreaming || cancelState.stopLabel === 'stopping')) {
      e.preventDefault()
      cancelState.cancelIfStreaming()
      return
    }

    // Block Enter submission while streaming — slash menu Enter still works below.
    if (e.key === 'Enter' && isStreaming && !slashMenu.slashOpen) {
      e.preventDefault()
      return
    }

    slashMenu.handleKeyDown(e)
  }

  return (
    <div
      className="relative"
      onDragOver={fileUpload.onDragOver}
      onDragLeave={fileUpload.onDragLeave}
      onDrop={fileUpload.onDrop}
    >
      {fileUpload.isDragging && (
        <div className="absolute inset-0 z-50 flex items-center justify-center bg-[var(--color-primary)]/80 border-2 border-dashed border-[var(--color-accent)] rounded-lg">
          <p className="text-[var(--color-accent)] font-medium">Drop files here</p>
        </div>
      )}

      {/* Fix 2: multi-phase reconnect banner.
          gave_up   → error banner with "Reconnect now" CTA (input locked).
          reconnecting/slow → amber pulsing indicator with attempt counter.
          null (connected) → nothing shown. */}
      {reconnectPhase === 'gave_up' && (
        <div
          data-testid="reconnect-banner"
          className="mb-2 rounded-lg px-3 py-2 bg-[var(--color-error)]/10 border border-[var(--color-error)]/20 flex items-center gap-2"
        >
          <WifiSlash size={14} className="text-[var(--color-error)] shrink-0" />
          <span className="text-xs text-[var(--color-error)] flex-1">
            Connection lost after all retry attempts.
          </span>
          <button
            type="button"
            onClick={reconnect}
            className="flex items-center gap-1 px-2 py-1 rounded text-xs font-medium bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30 transition-colors shrink-0"
            aria-label="Reconnect now"
          >
            <ArrowClockwise size={12} weight="bold" />
            Reconnect now
          </button>
        </div>
      )}
      {(reconnectPhase === 'reconnecting' || reconnectPhase === 'slow') && (
        <div
          data-testid="reconnect-banner"
          className="mb-2 text-xs text-[var(--color-warning)] flex items-center gap-1.5"
        >
          <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-warning)] inline-block animate-pulse" />
          {reconnectPhase === 'slow'
            ? `Reconnecting… (attempt ${reconnectAttempt} — slow retry)`
            : `Reconnecting… (attempt ${reconnectAttempt})`}
        </div>
      )}
      {!isConnected && reconnectPhase === null && (
        <div data-testid="reconnect-banner" className="mb-2 text-xs text-[var(--color-error)] flex items-center gap-1">
          <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-error)] inline-block" />
          Disconnected — reconnecting...
        </div>
      )}
      {/* Fix 3: outbound queue indicator — shown while messages are buffered
          (outboundQueue, while offline) or actively being drained one at a
          time after reconnect (pendingDrainQueue — see maybeDrainNext in
          store/chat.ts). Without the pendingDrainQueue branch this banner
          would disappear the instant reconnect fires even though several
          messages are still waiting for their turn to go out.
          Task 4 fix: on a flaky connection both arrays can be simultaneously
          non-empty (e.g. some messages bounced back to outboundQueue on a
          mid-drain disconnect while others are still parked in
          pendingDrainQueue — see the chat.ts store's own doc comments on
          drainOutboundQueue/maybeDrainNext). The previous if/else only ever
          showed ONE of the two counts, undercounting the true total still
          queued. Sum both counts whenever both are non-empty. */}
      {(outboundQueue.length > 0 || pendingDrainQueue.length > 0) && (
        <div
          data-testid="outbound-queue-indicator"
          className="mb-2 text-xs text-[var(--color-warning)] flex items-center gap-1.5"
        >
          <Clock size={12} className="shrink-0" />
          {(() => {
            const total = outboundQueue.length + pendingDrainQueue.length
            if (outboundQueue.length > 0 && pendingDrainQueue.length > 0) {
              return total === 1
                ? '1 message queued — will send on reconnect'
                : `${total} messages queued — will send on reconnect`
            }
            if (outboundQueue.length > 0) {
              return outboundQueue.length === 1
                ? '1 message queued — will send on reconnect'
                : `${outboundQueue.length} messages queued — will send on reconnect`
            }
            return pendingDrainQueue.length === 1
              ? '1 queued message sending…'
              : `${pendingDrainQueue.length} queued messages sending…`
          })()}
        </div>
      )}

      {/* Slash command + skills partitioned dropdown (FR-005).
          F6: unified slashItems.map() — emits a section header on each section
          transition, making the `section` field load-bearing and removing the
          duplicate render blocks + the off-by-one `globalIndex` variable.
          FR-014/R3: skill items show their argument_hint as muted help text. */}
      {slashMenu.shouldShowSlash && slashMenu.slashOpen && (
        <div
          data-testid="slash-menu"
          className="mb-2 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] overflow-hidden shadow-lg"
        >
          {slashMenu.slashItems.map((item, globalIndex) => {
            const prevSection = globalIndex > 0 ? slashMenu.slashItems[globalIndex - 1].section : null
            const isFirstInSection = item.section !== prevSection
            return (
              <React.Fragment key={item.key}>
                {isFirstInSection && (
                  <div className={cn(
                    'px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]',
                    globalIndex === 0
                      ? 'border-b border-[var(--color-border)]'
                      : 'border-t border-[var(--color-border)]',
                  )}>
                    {item.section === 'commands' ? 'Commands' : 'Skills'}
                  </div>
                )}
                <button
                  type="button"
                  className={cn(
                    'w-full flex items-baseline gap-3 px-3 py-2 text-left transition-colors',
                    globalIndex === slashMenu.slashHighlight
                      ? 'bg-[var(--color-accent)]/10 text-[var(--color-secondary)]'
                      : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-secondary)]',
                  )}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    item.onSelect()
                  }}
                  onMouseEnter={() => slashMenu.onHoverItem(globalIndex)}
                >
                  <span className="font-mono text-xs text-[var(--color-accent)]">{item.label}</span>
                  <span className="text-[11px]">{item.description}</span>
                  {/* FR-014/R3: show argument_hint as muted help text for skills */}
                  {item.section === 'skills' && item.argumentHint && (
                    <span className="ml-auto text-[10px] text-[var(--color-muted)] opacity-70 font-mono shrink-0">
                      {item.argumentHint}
                    </span>
                  )}
                </button>
              </React.Fragment>
            )
          })}
        </div>
      )}

      {/* Pending attachments — native AssistantUI composer attachments. Shows a
          chip for each attached file (via paperclip, drag-drop, or paste); the
          AttachmentAdapter (src/lib/attachment-adapter.ts) uploads them on send
          and threads the media:// ref into our transport via onNew. */}
      <div className="flex flex-wrap gap-2 px-2 empty:hidden [&:has(*)]:pb-2">
        <ComposerPrimitive.Attachments components={{ Attachment: ComposerAttachmentChip }} />
      </div>

      <div className="flex items-end gap-2 px-2 py-2">
        {/* Attach button — native; opens the file picker scoped to the adapter's
            accept list. Replaces the old custom paperclip + hidden <input>. */}
        <ComposerPrimitive.AddAttachment
          disabled={!isConnected || isStreaming || isReplaying || reconnectPhase === 'gave_up'}
          className="shrink-0 w-11 h-11 rounded-xl flex items-center justify-center text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
          aria-label="Attach file"
          title="Attach file"
        >
          <Paperclip size={16} />
        </ComposerPrimitive.AddAttachment>

      <ComposerPrimitive.Root
        className="flex items-end gap-2 flex-1"
        onSubmit={(e) => {
          // Block Enter-submit while streaming; slash-menu Enter is handled in handleKeyDown.
          if (isStreaming) {
            e.preventDefault()
            return
          }
          // Send-path interception: if the typed text is exactly a client-delivery
          // slash command (e.g. "/clear", "/help", "/model", "/cancel"), handle it
          // locally and prevent it from reaching the backend. This converges the
          // typed+Enter path with the palette selection path.
          if (slashMenu.interceptClientCommand()) {
            e.preventDefault()
          }
        }}
      >
        {/* Ghost text wrapper — positioned overlay approach */}
        <div className="relative flex-1">
        <ComposerPrimitive.Input
          data-testid="chat-input"
          placeholder={agentRemoved ? 'Agent has been removed — this session is read-only' : composerPlaceholder(isConnected || reconnectPhase === 'reconnecting' || reconnectPhase === 'slow', isStreaming, isReplaying, activeAgentName, reconnectPhase === 'gave_up')}
          // FR-3a: the slash menu must be reachable mid-stream, which means the
          // textarea has to accept keystrokes during streaming. Submission is
          // blocked elsewhere: the ComposerPrimitive.Root's onSubmit handler
          // above preventDefaults while isStreaming, and the composer's
          // onKeyDown handler (handleKeyDown) swallows Enter while streaming
          // unless the slash menu is open. So gate visual-only
          // (cursor-not-allowed/opacity) on isStreaming via the className
          // below, not the disabled attribute.
          disabled={!inputEnabled}
          aria-disabled={!inputEnabled || isStreaming || undefined}
          rows={1}
          cancelOnEscape={false}
          className={cn(
            'w-full resize-none rounded-2xl border border-[var(--color-border)] bg-[var(--color-surface-2)] px-4 py-2.5 text-sm text-[var(--color-secondary)] outline-none',
            'placeholder:text-[var(--color-muted)] min-h-[24px] max-h-[200px] leading-6 overflow-hidden',
            'focus:border-[var(--color-accent)]/50 focus:ring-1 focus:ring-[var(--color-accent)]/20',
            (!inputEnabled || isStreaming) && 'opacity-60 cursor-not-allowed',
          )}
          aria-label="Message input"
          onChange={(e) => {
            const val = (e.target as HTMLTextAreaElement).value
            slashMenu.onInputChange(val)
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
          onBlur={slashMenu.onInputBlur}
          onPaste={fileUpload.onPaste}
        />
        {/* Ghost text overlay — shown when value is exactly `/<skillId> ` after skill selection.
            F3-frontend/FR-006/R3: shows the skill's argument_hint when declared, else `<message>`.
            The hint comes from the skill selected from the menu. */}
        {slashMenu.showGhostText && (
          <div
            aria-hidden="true"
            className="pointer-events-none absolute inset-0 px-4 py-2.5 text-sm leading-6 flex items-start"
            data-testid="ghost-text"
          >
            <span className="invisible whitespace-pre">{slashMenu.inputValue}</span>
            <span className="text-[var(--color-muted)] opacity-60">
              {slashMenu.ghostText}
            </span>
          </div>
        )}
        </div>

        {isStreaming || cancelState.stopLabel === 'stopping' ? (
          <button
            type="button"
            data-testid="stop-btn"
            onClick={() => {
              // EC-15 / FR-21: cancelUnconditional() sets the label synchronously
              // so the UI updates within the same React render tick, before the
              // cancel network round-trip starts (no perceived latency). It does
              // NOT guard on isStreaming — see useCancelState's doc comment for
              // why guarding here would silently no-op on the render/click race.
              cancelState.cancelUnconditional()
            }}
            className={cn(
              'shrink-0 rounded-xl flex items-center justify-center transition-colors',
              cancelState.stopLabel === 'stopping'
                ? 'px-3 h-11 gap-1.5 text-xs font-medium bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30'
                : 'w-11 h-11',
              isStreaming
                ? 'bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30'
                : 'bg-[var(--color-surface-3)] text-[var(--color-muted)] cursor-wait',
            )}
            aria-label={cancelState.stopLabel === 'stopping' ? 'Stopping...' : 'Stop generation'}
            title="Stop (Escape)"
          >
            <Stop size={15} weight="fill" />
            {cancelState.stopLabel === 'stopping' && <span>Stopping...</span>}
          </button>
        ) : (
          // FR-I-014: also disabled during replay so user cannot send out-of-order.
          // Fix 3: when reconnecting (fast or slow), allow send — messages go to
          // the outbound queue and drain automatically on reconnect.
          //
          // Native ComposerPrimitive.Send: calls composer.send() → onNew, which
          // now carries text AND attachments. Send and Enter both go through
          // composer.send(), so they are identical. Send auto-disables when the
          // composer is empty (no text and no attachments) via the runtime's
          // canSend, so no manual empty-check is needed here.
          <ComposerPrimitive.Send
            disabled={!inputEnabled || isReplaying}
            data-testid="chat-send"
            className={cn(
              'shrink-0 w-11 h-11 rounded-xl flex items-center justify-center transition-colors',
              inputEnabled && !isReplaying
                ? reconnectPhase === 'reconnecting' || reconnectPhase === 'slow'
                  // Muted accent while reconnecting — functional but visually signals
                  // the message will queue rather than send immediately.
                  ? 'bg-[var(--color-accent)]/50 text-[var(--color-primary)] hover:bg-[var(--color-accent)]/70'
                  : 'bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent-hover)] disabled:bg-[var(--color-surface-3)] disabled:text-[var(--color-muted)] disabled:cursor-not-allowed'
                : 'bg-[var(--color-surface-3)] text-[var(--color-muted)] cursor-not-allowed',
            )}
            aria-label={
              reconnectPhase === 'reconnecting' || reconnectPhase === 'slow'
                ? 'Queue message (will send on reconnect)'
                : 'Send message'
            }
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

      {/* Harmful-file upload double-confirm — replaces the native window.confirm pair.
          Stage 1 warns and lists the flagged files; stage 2 is the second
          confirmation. Files are only attached after the user confirms stage 2. */}
      <AlertDialog
        open={fileUpload.harmfulConfirm !== null}
        onOpenChange={(open) => {
          if (!open) fileUpload.dismissHarmfulConfirm()
        }}
      >
        <AlertDialogContent>
          {fileUpload.harmfulStage === 1 ? (
            <>
              <AlertDialogHeader>
                <AlertDialogTitle>Potentially harmful file(s)</AlertDialogTitle>
                <AlertDialogDescription>
                  {fileUpload.harmfulConfirm
                    ? `${fileUpload.harmfulConfirm.harmfulNames.join(', ')} may be potentially harmful file(s).\n\nAre you sure you want to upload?`
                    : ''}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction
                  variant="destructive"
                  onClick={fileUpload.advanceHarmfulStage}
                >
                  Continue
                </AlertDialogAction>
              </AlertDialogFooter>
            </>
          ) : (
            <>
              <AlertDialogHeader>
                <AlertDialogTitle>Confirm upload</AlertDialogTitle>
                <AlertDialogDescription>
                  {fileUpload.harmfulConfirm
                    ? `Please confirm again: Upload ${fileUpload.harmfulConfirm.harmfulNames.length} potentially harmful file(s)?`
                    : ''}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction
                  variant="destructive"
                  onClick={fileUpload.confirmHarmfulUpload}
                >
                  Upload
                </AlertDialogAction>
              </AlertDialogFooter>
            </>
          )}
        </AlertDialogContent>
      </AlertDialog>
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
    // '__pending' is the optimistic-send sentinel (store/chat.ts) used before the
    // real session_id arrives over WS — it has no server-side history, so fetching
    // it 404s (spurious console errors + a historyError that tears down the composer).
    // Mirror the same guard used in attachment-adapter.ts.
    enabled: !!activeSessionId && activeSessionId !== '__pending',
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

  const liteMode = useConnectionStore((s) => s.liteMode)

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

          {/* Virtualized message list — only visible rows (+ 5-message overscan) are mounted as DOM nodes. */}
          {messages.length === 0 ? (
            <div className="flex-1 overflow-y-auto pt-4 pb-2">
              <WelcomeState hasAgent={!!activeAgentId} />
            </div>
          ) : (
            <VirtualizedMessageList messages={messages} liteMode={liteMode} />
          )}

          {/* FR-21: Interrupted-message status markers — rendered inside
              ThreadPrimitive.Root but OUTSIDE the scrollable Viewport. This
              position has guaranteed non-zero height because it's in the
              non-scrolling flex layout between the Viewport and the composer.
              Playwright locates these elements via text=(interrupted). */}
          <InterruptedMessageMarkers />

          {/* Rate-limit indicator — shown above composer. Tool-approval requests
              (including `bash`) are handled by the global ToolApprovalModal
              (ADR-036 §3.4 retired the dedicated exec-only approval flow). */}
          {rateLimitEvent && (
            <div className="px-4 space-y-2 pb-2">
              <RateLimitIndicator
                scope={rateLimitEvent.scope}
                resource={rateLimitEvent.resource}
                policyRule={rateLimitEvent.policyRule}
                retryAfterSeconds={rateLimitEvent.retryAfterSeconds}
                tool={rateLimitEvent.tool}
                onDismiss={clearRateLimitEvent}
              />
            </div>
          )}

          {/* Activity Bar — persistent strip showing live background agent/shell
              activity (delegate spans + background bash runs). Opens a slide-out
              detail panel on click. See src/components/chat/ActivityBar.tsx. */}
          <div className="px-4 pb-2">
            <ActivityBar />
          </div>

          {/* Composer — centered, ChatGPT-style floating layout */}
          <div className="relative w-full">
            {/* Gradient fade above composer */}
            <div className="absolute -top-8 left-0 right-0 h-8 bg-gradient-to-t from-[var(--color-primary)] to-transparent pointer-events-none" />
            <div className="w-full max-w-3xl mx-auto px-4 pt-2 pb-[max(0.5rem,env(safe-area-inset-bottom))]">
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
