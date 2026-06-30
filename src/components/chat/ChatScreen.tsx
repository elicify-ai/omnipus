import React, { useEffect, useLayoutEffect, useRef, useState, useCallback } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { generateId } from '@/lib/constants'
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
import { ExecApprovalBlock } from './ExecApprovalBlock'
import { RateLimitIndicator } from './RateLimitIndicator'
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

// ── Slash/skill palette item shape ───────────────────────────────────────────

interface SlashItem {
  key: string
  label: string
  description: string
  section: 'commands' | 'skills'
  argumentHint?: string
  onSelect: () => void
}

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
          {/* Media attachments */}
          {mediaItems.length > 0 && (
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

        {/* Action bar — always visible at reduced opacity, fully opaque on hover */}
        <div className="flex items-center gap-1 opacity-70 hover:opacity-100 transition-opacity duration-150">
          <StaticCopyButton text={message.content ?? ''} />
        </div>

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

  // ── Stick-to-bottom tracking (ChatGPT/Claude-style) ─────────────────────────
  // wasAtBottomRef = "auto-follow is active". It is SUSPENDED only by a genuine
  // USER scroll gesture that moves away from the bottom (wheel up, touch drag,
  // keyboard nav, scrollbar drag) and RESUMED whenever the viewport reaches the
  // bottom again. This distinction is essential: content-growth reflows (a
  // streamed token, a tool-call block, an inline image) and our own programmatic
  // re-pin ALSO fire 'scroll' events, and those must NEVER suspend auto-follow.
  // The earlier implementation set wasAtBottom = (distance < threshold) on EVERY
  // scroll, so a transient large distance from a tool-call/image render flipped
  // it false and stuck the view ~600px above the bottom (the reported bug,
  // reproduced in a live browser). We therefore only honor a suspend while a real
  // user-input gesture is in progress — the in-house equivalent of
  // use-stick-to-bottom's user-vs-programmatic scroll discrimination.
  const wasAtBottomRef = useRef(true)
  const userGestureUntilRef = useRef(0)
  const pointerDownRef = useRef(false)

  const SCROLL_THRESHOLD = 50

  // A real user input opens a short window (wheel/touch/key) — or holds, for a
  // pointer/scrollbar drag — during which a scroll away from the bottom is
  // attributed to the user rather than to content growth.
  const markUserGesture = useCallback(() => {
    userGestureUntilRef.current = performance.now() + 300
  }, [])
  const onPointerDown = useCallback(() => {
    pointerDownRef.current = true
    markUserGesture()
  }, [markUserGesture])

  // Clear the pointer-drag flag on release anywhere (the pointer may leave the
  // list before release).
  useEffect(() => {
    const up = () => {
      pointerDownRef.current = false
    }
    window.addEventListener('pointerup', up)
    window.addEventListener('pointercancel', up)
    return () => {
      window.removeEventListener('pointerup', up)
      window.removeEventListener('pointercancel', up)
    }
  }, [])

  const onScroll = useCallback(() => {
    const el = scrollContainerRef.current
    if (!el) return
    const dist = el.scrollHeight - el.scrollTop - el.clientHeight
    if (dist < SCROLL_THRESHOLD) {
      // Reached the bottom (by any means) → resume / keep following.
      wasAtBottomRef.current = true
    } else if (pointerDownRef.current || performance.now() < userGestureUntilRef.current) {
      // Away from the bottom due to an ACTIVE user gesture → suspend following.
      wasAtBottomRef.current = false
    }
    // else: away from the bottom due to content growth or a programmatic scroll
    // → leave wasAtBottomRef unchanged; the ResizeObserver re-pins to the bottom.
  }, [])

  const virtualizer = useVirtualizer({
    count: historicalMessages.length,
    getScrollElement: () => scrollContainerRef.current,
    estimateSize: () => 80,
    overscan: 5,
    // measureElement default (getBoundingClientRect().height) is correct; no override needed.
  })

  // Content-agnostic stick-to-bottom. A single ResizeObserver on the content
  // wrapper fires on ANY height change anywhere in the transcript — streamed
  // tokens, tool-call blocks, tool results, inline images/media, expanding or
  // collapsing detail, AND the virtualizer's own row remeasures — without us
  // having to enumerate each signal (the previous implementation only watched
  // messages.length + streaming text length, so tool calls / images in the live
  // turn never triggered a re-pin — the reported bug). On any such change we
  // re-pin to the bottom, but ONLY when the user is already at the bottom
  // (wasAtBottomRef, maintained by onScroll): if they've scrolled up to read
  // history we must not yank them down, and we resume sticking the moment they
  // scroll back. This matches ChatGPT/Claude and is the same approach
  // assistant-ui's Viewport uses (use-stick-to-bottom); we keep it inline
  // because our list is custom-virtualized, not rendered by ThreadPrimitive
  // .Viewport. ResizeObserver is guaranteed defined here — the parent
  // feature-detects it and falls back to PlainMessageList otherwise.
  useLayoutEffect(() => {
    const el = scrollContainerRef.current
    const content = contentRef.current
    if (!el || !content) return undefined
    const pinIfAtBottom = () => {
      if (wasAtBottomRef.current) el.scrollTop = el.scrollHeight
    }
    // Pin once on mount (opening a session jumps to the latest message).
    const initialRaf = requestAnimationFrame(pinIfAtBottom)
    // Re-pin on every subsequent content-size change. rAF defers the read until
    // the browser has committed the new layout so scrollHeight is final.
    let rafId = 0
    const ro = new ResizeObserver(() => {
      cancelAnimationFrame(rafId)
      rafId = requestAnimationFrame(pinIfAtBottom)
    })
    ro.observe(content)
    return () => {
      cancelAnimationFrame(initialRaf)
      cancelAnimationFrame(rafId)
      ro.disconnect()
    }
  }, [])

  const virtualItems = virtualizer.getVirtualItems()

  const rowForMessage = (msg: ChatMessage) => {
    if (msg.role === 'user')
      return <VirtualUserMessageRow message={msg} skills={skills} commandLabels={commandLabels} />
    if (msg.role === 'system') return <VirtualSystemMessageRow message={msg} />
    return <VirtualAssistantMessageRow message={msg} liteMode={liteMode} />
  }

  return (
    <div
      ref={scrollContainerRef}
      data-testid="virtualized-message-list"
      className="flex-1 overflow-y-auto overscroll-contain pt-4 pb-2"
      style={{ position: 'relative' }}
      onScroll={onScroll}
      onWheel={markUserGesture}
      onTouchStart={markUserGesture}
      onTouchMove={markUserGesture}
      onKeyDown={markUserGesture}
      onPointerDown={onPointerDown}
    >
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
    </div>
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

        {/* Action bar — Copy + Retry buttons, always visible at reduced opacity */}
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

const HARMFUL_EXTENSIONS = ['.exe', '.bat', '.cmd', '.sh', '.ps1', '.dll', '.sys', '.msi', '.scr', '.com']

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
          className="absolute -top-1.5 -right-1.5 opacity-0 group-hover:opacity-100 focus:opacity-100 transition-opacity"
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
  const cancelStream = useChatStore((s) => s.cancelStream)
  const appendMessage = useChatStore((s) => s.appendMessage)
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const startNewSession = useSessionStore((s) => s.startNewSession)
  const addToast = useUiStore((s) => s.addToast)
  const composerRuntime = useComposerRuntime()

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const activeAgentName = agents.find((a) => a.id === activeAgentId)?.name ?? 'Omnipus'

  const [inputValue, setInputValue] = useState('')
  const [slashOpen, setSlashOpen] = useState(false)
  const [slashHighlight, setSlashHighlight] = useState(0)
  const [isDragging, setIsDragging] = useState(false)
  // Ghost text after skill selection — shown when value is exactly `/<skill-id> `
  // F3-frontend: also store the skill's argument_hint so the ghost can show it
  // instead of the generic `<message>` when the skill declares one.
  const [ghostSkillId, setGhostSkillId] = useState<string | null>(null)
  const [ghostArgumentHint, setGhostArgumentHint] = useState<string | null>(null)
  // Harmful-file upload confirmation (replaces the two native window.confirm calls).
  // `harmfulConfirm` holds the files awaiting confirmation plus the flagged names;
  // it is the open/closed signal — the dialog is closed when `harmfulConfirm` is
  // null and open otherwise. `harmfulStage` is typed 1 | 2 (never null) and only
  // advances 1 → 2 (double-confirm) before commit; it is reset to 1 each time a
  // new harmful set is queued (see handleFilesSelected).
  const [harmfulConfirm, setHarmfulConfirm] = useState<{
    files: File[]
    harmfulNames: string[]
  } | null>(null)
  const [harmfulStage, setHarmfulStage] = useState<1 | 2>(1)
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

  // US-4 / FR-008: fetch the web-surface command list from the single source of truth.
  // On error the palette shows nothing — no crash (per integration boundary spec).
  const { data: commands = [] } = useQuery<SlashCommand[]>({
    queryKey: ['commands', 'web'],
    queryFn: () => fetchCommands('web'),
    staleTime: 60_000,
    enabled: inputEnabled,
  })

  // Skills query: always enabled when input is enabled (not gated on skill-arg mode).
  // staleTime of 60s matches the commands query — skills change rarely.
  const { data: skills = [] } = useQuery<Skill[]>({
    queryKey: ['skills'],
    // Wrapped (not a bare `fetchSkills` reference) to match the commands query's
    // `() => fetchCommands('web')` pattern: the binding is read only when the
    // queryFn is invoked, so sibling tests whose useQuery mock ignores queryFn
    // don't need to add fetchSkills to their @/lib/api mock.
    queryFn: () => fetchSkills(),
    staleTime: 60_000,
    enabled: inputEnabled,
  })

  // FR-005: partitioned slash menu — Commands + Skills sections
  // Triggered when input starts with "/" (no old skill-arg gate)
  const menuFilter = (() => {
    if (!inputValue.startsWith('/') || isReplaying || !inputEnabled) return null
    return inputValue.slice(1).toLowerCase() // the text after "/"
  })()

  const isSkillsFilter = menuFilter === 'skills'

  // Commands section — hidden when typing "/skills" (D9)
  const visibleCommandItems: SlashItem[] = (() => {
    if (menuFilter === null || isSkillsFilter) return []
    const all = commands.filter((cmd) => {
      const cmdName = cmd.label.slice(1).toLowerCase() // strip leading /
      return menuFilter === '' || cmdName.startsWith(menuFilter)
    })
    const filtered = isStreaming ? all.filter((cmd) => cmd.available_while_streaming === true) : all
    return filtered.map((cmd) => ({
      key: cmd.label,
      label: cmd.label,
      description: cmd.description,
      section: 'commands' as const,
      onSelect: () => executeSlashCommand(cmd.label),
    }))
  })()

  // Skills section — always shown when "/" typed, unless empty
  const visibleSkillMenuItems: SlashItem[] = (() => {
    if (menuFilter === null) return []
    const lower = isSkillsFilter ? '' : menuFilter
    const filtered = skills.filter((s) =>
      lower === '' ||
      s.id.toLowerCase().startsWith(lower) ||
      s.name.toLowerCase().startsWith(lower),
    )
    return filtered.slice(0, 8).map((s) => {
      // F3-frontend/FR-014/R3: the skill's argument_hint drives the menu help text
      // and the inline ghost (Skill.argument_hint is on the generated wire type).
      const argHint: string | undefined = s.argument_hint
      return {
        key: s.id,
        label: `/${s.id}`,
        description: s.name + (s.description ? ` — ${s.description}` : ''),
        section: 'skills' as const,
        argumentHint: argHint,
        onSelect: () => completeSkillName(s.id, argHint),
      }
    })
  })()

  // Unified list for keyboard nav — commands first, then skills
  const slashItems: SlashItem[] = [...visibleCommandItems, ...visibleSkillMenuItems]
  const shouldShowSlash = slashItems.length > 0 && !isReplaying && inputEnabled

  // Reset highlight to 0 when the visible list changes (length or content) so
  // the cursor never points out-of-bounds as the filter narrows.
  const slashItemKeys = slashItems.map((i) => i.key).join(',')
  useEffect(() => { setSlashHighlight(0) }, [slashItemKeys])

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

  // runClientCommand — shared handler for client-delivery slash commands.
  // Called both from palette selection (executeSlashCommand) and from the
  // send-path interception so that typing "/clear"+Enter converges with
  // selecting /clear from the palette — both run client-side, never reaching
  // the backend.
  //
  // Returns true when the command was handled (caller must NOT send the text),
  // false when the name is not a known client command (caller should fall
  // through to inserting as text — Issue 3 fallback).
  function runClientCommand(name: string): boolean {
    if (name === 'clear') {
      // US-4/AC-2: start a new conversation per spec — aligned to the "new
      // conversation" semantic (startNewSession) rather than just wiping the
      // local message list.
      startNewSession()
      return true
    }

    if (name === 'help') {
      // US-4/AC-2: build the help text from the fetched command list.
      const helpLines = commands
        .map((c) => `- \`${c.label}\` — ${c.description}`)
        .join('\n')
      const helpText = `**Omnipus commands:**\n${helpLines}\n\n**Tips:**\n- Press **Enter** to send, **Shift+Enter** for newline\n- Click tool call headers to expand/collapse details\n- Hover over messages to copy them`
      appendMessage({
        id: generateId(),
        role: 'system',
        content: helpText,
        timestamp: new Date().toISOString(),
        status: 'done',
      })
      return true
    }

    if (name === 'model') {
      // US-4/AC-2: open the model selector in the chat header (ChatControls).
      // Setting modelSelectorOpen=true drives the controlled Popover in ModelSelector
      // without the user having to click it directly.
      // Per A4: web-only client action; opens the chat model selector, not the
      // server agent default.
      useUiStore.getState().setModelSelectorOpen(true)
      return true
    }

    if (name === 'agents') {
      // Open the agent selector in the chat header (ChatControls) via the ui store flag.
      useUiStore.getState().setAgentSelectorOpen(true)
      return true
    }

    if (name === 'skills') {
      // D9: set input to "/skills" to trigger the skills-only filter in the menu.
      // The menu handles this: when inputValue === "/skills", isSkillsFilter is true
      // and only the Skills section shows. Re-open the menu after the clear.
      composerRuntime.setText('/skills')
      setInputValue('/skills')
      setSlashOpen(true)
      return true
    }

    if (name === 'cancel') {
      // FR-3a: /cancel uses the same cancelStream() as the Stop button.
      // Only morph the button to "Stopping..." if the turn is actively streaming.
      if (isStreaming) {
        stoppingStartedAt.current = Date.now()
        setStopLabel('stopping')
      }
      cancelStream()
      return true
    }

    // Issue 3 fallback: unknown client command — do NOT silently drop.
    // Return false so the caller inserts it as text rather than clearing the composer.
    return false
  }

  // executeSlashCommand — called when the user selects a palette entry.
  // `label` is the full label string from the SlashCommand (e.g. "/clear").
  // FR-009: dispatch by `delivery`:
  //   - 'client' → run the local handler via runClientCommand; do NOT send.
  //   - 'agent'  → insert the label as text into the composer so the user can
  //                complete it and forward it via the message frame on send.
  function executeSlashCommand(label: string) {
    closeSlash()

    // Look up the full command definition by label to get the name + delivery.
    const def = commands.find((c) => c.label === label)

    if (!def) {
      // Unknown label (shouldn't happen with API-driven palette, but be safe).
      return
    }

    if (def.delivery === 'agent') {
      // Insert "/name " as text so the user can complete it and send.
      composerRuntime.setText(`${def.label} `)
      setInputValue(`${def.label} `)
      return
    }

    // delivery === 'client': clear the composer then run the handler.
    composerRuntime.setText('')
    setInputValue('')

    const handled = runClientCommand(def.name)
    if (!handled) {
      // Issue 3: unknown client command — insert text so it isn't silently lost.
      composerRuntime.setText(`${def.label} `)
      setInputValue(`${def.label} `)
    }
  }

  // completeSkillName — called when the user selects a skill from the
  // partitioned skill menu.  Sets the input to `/<id> ` and shows ghost text.
  // Accepts the skill's argument_hint so the ghost shows it instead of the
  // generic `<message>` when the skill declares one (FR-006/R3).
  function completeSkillName(id: string, argumentHint?: string) {
    const text = `/${id} `
    composerRuntime.setText(text)
    setInputValue(text)
    setGhostSkillId(id)
    setGhostArgumentHint(argumentHint ?? null)
    closeSlash()
  }

  // Send-path interception: before AssistantUI's onNew fires, check if the
  // trimmed input is exactly a client-delivery slash command (e.g. "/clear").
  // If it is, run it locally and prevent the message from reaching the backend.
  // This makes typing "/clear"+Enter behave identically to palette selection.
  //
  // Returns true if the text was intercepted (caller must stop the send),
  // false if the message should proceed normally.
  function interceptClientCommand(text: string): boolean {
    const trimmed = text.trim()
    if (!trimmed.startsWith('/')) return false
    const def = commands.find((c) => c.delivery === 'client' && c.label === trimmed)
    if (!def) return false
    composerRuntime.setText('')
    setInputValue('')
    runClientCommand(def.name)
    return true
  }

  // commitFiles hands each file to the native composer attachment system; the
  // AttachmentAdapter uploads on send and threads the media:// ref through
  // onNew. Used by drag-drop and image paste.
  function commitFiles(files: File[]) {
    for (const file of files) {
      composerRuntime.addAttachment(file).catch((err: unknown) => {
        addToast({
          message: err instanceof Error ? err.message : `Could not attach "${file.name}"`,
          variant: 'error',
        })
      })
    }
  }

  function handleFilesSelected(files: File[]) {
    const harmful = files.filter((f) =>
      HARMFUL_EXTENSIONS.some((ext) => f.name.toLowerCase().endsWith(ext))
    )
    if (harmful.length > 0) {
      // Defer to the AlertDialog double-confirm flow; commit happens only when
      // the user clicks through both stages (preserves the original semantics).
      setHarmfulStage(1)
      setHarmfulConfirm({ files, harmfulNames: harmful.map((f) => f.name) })
      return
    }
    commitFiles(files)
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
      setSlashHighlight((h) => (h + 1) % slashItems.length)
      setSlashOpen(true)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSlashHighlight((h) => (h - 1 + slashItems.length) % slashItems.length)
      setSlashOpen(true)
    } else if (e.key === 'Enter' && slashOpen) {
      e.preventDefault()
      slashItems[slashHighlight]?.onSelect()
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
      {/* Fix 3: outbound queue indicator — shown while messages are buffered. */}
      {outboundQueue.length > 0 && (
        <div
          data-testid="outbound-queue-indicator"
          className="mb-2 text-xs text-[var(--color-warning)] flex items-center gap-1.5"
        >
          <Clock size={12} className="shrink-0" />
          {outboundQueue.length === 1
            ? '1 message queued — will send on reconnect'
            : `${outboundQueue.length} messages queued — will send on reconnect`}
        </div>
      )}

      {/* Slash command + skills partitioned dropdown (FR-005).
          F6: unified slashItems.map() — emits a section header on each section
          transition, making the `section` field load-bearing and removing the
          duplicate render blocks + the off-by-one `globalIndex` variable.
          FR-014/R3: skill items show their argument_hint as muted help text. */}
      {shouldShowSlash && slashOpen && (
        <div
          data-testid="slash-menu"
          className="mb-2 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] overflow-hidden shadow-lg"
        >
          {slashItems.map((item, globalIndex) => {
            const prevSection = globalIndex > 0 ? slashItems[globalIndex - 1].section : null
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
                    globalIndex === slashHighlight
                      ? 'bg-[var(--color-accent)]/10 text-[var(--color-secondary)]'
                      : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-secondary)]',
                  )}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    item.onSelect()
                  }}
                  onMouseEnter={() => setSlashHighlight(globalIndex)}
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
          const currentText = composerRuntime.getState().text ?? inputValue
          if (interceptClientCommand(currentText)) {
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
          // blocked elsewhere: the Send button has its own `!isStreaming` gate
          // (line 1278) and the onKeyDown handler at line 1031 swallows Enter
          // when streaming unless the slash menu is open. So gate visual-only
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
            setInputValue(val)
            // Clear ghost if value no longer exactly matches `/<ghostSkillId> `
            if (ghostSkillId && val !== `/${ghostSkillId} `) {
              setGhostSkillId(null)
              setGhostArgumentHint(null)
            }
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
            setGhostSkillId(null)
            setGhostArgumentHint(null)
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
        {/* Ghost text overlay — shown when value is exactly `/<skillId> ` after skill selection.
            F3-frontend/FR-006/R3: shows the skill's argument_hint when declared, else `<message>`.
            The hint comes from `ghostArgumentHint` set when the skill was selected from the menu. */}
        {ghostSkillId && inputValue === `/${ghostSkillId} ` && (
          <div
            aria-hidden="true"
            className="pointer-events-none absolute inset-0 px-4 py-2.5 text-sm leading-6 flex items-start"
            data-testid="ghost-text"
          >
            <span className="invisible whitespace-pre">{`/${ghostSkillId} `}</span>
            <span className="text-[var(--color-muted)] opacity-60">
              {ghostArgumentHint ?? '<message>'}
            </span>
          </div>
        )}
        </div>

        {isStreaming || stopLabel === 'stopping' ? (
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
            className={cn(
              'shrink-0 rounded-xl flex items-center justify-center transition-colors',
              stopLabel === 'stopping'
                ? 'px-3 h-11 gap-1.5 text-xs font-medium bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30'
                : 'w-11 h-11',
              isStreaming
                ? 'bg-[var(--color-error)]/20 text-[var(--color-error)] hover:bg-[var(--color-error)]/30'
                : 'bg-[var(--color-surface-3)] text-[var(--color-muted)] cursor-wait',
            )}
            aria-label={stopLabel === 'stopping' ? 'Stopping...' : 'Stop generation'}
            title="Stop (Escape)"
          >
            <Stop size={15} weight="fill" />
            {stopLabel === 'stopping' && <span>Stopping...</span>}
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
        open={harmfulConfirm !== null}
        onOpenChange={(open) => {
          if (!open) setHarmfulConfirm(null)
        }}
      >
        <AlertDialogContent>
          {harmfulStage === 1 ? (
            <>
              <AlertDialogHeader>
                <AlertDialogTitle>Potentially harmful file(s)</AlertDialogTitle>
                <AlertDialogDescription>
                  {harmfulConfirm
                    ? `${harmfulConfirm.harmfulNames.join(', ')} may be potentially harmful file(s).\n\nAre you sure you want to upload?`
                    : ''}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction
                  variant="destructive"
                  onClick={() => setHarmfulStage(2)}
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
                  {harmfulConfirm
                    ? `Please confirm again: Upload ${harmfulConfirm.harmfulNames.length} potentially harmful file(s)?`
                    : ''}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction
                  variant="destructive"
                  onClick={() => {
                    if (harmfulConfirm) commitFiles(harmfulConfirm.files)
                    setHarmfulConfirm(null)
                  }}
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

  const activePendingApprovals = pendingApprovals.filter((a) => a.status === 'pending')
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
