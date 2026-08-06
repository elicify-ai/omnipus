import ReactMarkdown from 'react-markdown'
import type { Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { User, Robot } from '@phosphor-icons/react'
import { useQuery } from '@tanstack/react-query'
import { ToolCallBadge } from './ToolCallBadge'
import { ModelFooter } from './ModelFooter'
import { IconRenderer } from '@/components/shared/IconRenderer'
import type { ChatMessage, PositionedToolCall } from '@/store/chat'
import { useChatStore } from '@/store/chat'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { fetchAgents } from '@/lib/api'
import { cn } from '@/lib/utils'
import { splitMessageParts } from '@/lib/messageParts'

// ADR-051 — cap on the verbose-only "Technical details" disclosure content.
// Keeps a runaway provider error payload from blowing out the chat scroll.
// Matches the cap in ChatScreen.tsx's VirtualAssistantMessageRow (parity).
const ERROR_DETAIL_MAX_CHARS = 512

interface MessageItemProps {
  message: ChatMessage
}

// Hoisted so it isn't recreated per render — and, since a message can now
// render more than one ReactMarkdown instance (one per interleaved text
// slice — see splitMessageParts below), not recreated per slice either.
// Renderers are pure (no closure over component state), so a module-level
// constant is safe.
const markdownComponents = {
  code({ children, className }) {
    const isInline = !className
    if (isInline) {
      return (
        <code className="font-mono text-[11px] bg-[var(--color-surface-2)] px-1.5 py-0.5 rounded text-[var(--color-accent)]">
          {children}
        </code>
      )
    }
    return (
      <pre className="bg-[var(--color-surface-2)] rounded-md p-3 overflow-x-auto my-2">
        <code className="font-mono text-[11px] text-[var(--color-secondary)] block">
          {children}
        </code>
      </pre>
    )
  },
  a({ href, children }) {
    // Block executable URL schemes to prevent XSS via markdown links.
    // javascript: runs script; data: can encode an HTML payload with
    // embedded <script>; vbscript: is legacy IE but still flagged by CodeQL
    // js/unsafe-jquery-plugin and similar rules. Whitespace prefixes are
    // stripped before the comparison because browsers tolerate them in URLs.
    const trimmed = href?.trim().toLowerCase() ?? ''
    if (
      trimmed.startsWith('javascript:') ||
      trimmed.startsWith('data:') ||
      trimmed.startsWith('vbscript:')
    ) {
      return <span>{children}</span>
    }
    return (
      <a tabIndex={0}
        href={href}
        target="_blank"
        rel="noopener noreferrer"
        className="text-[var(--color-accent)] underline underline-offset-2 hover:text-[var(--color-accent-hover)]"
      >
        {children}
      </a>
    )
  },
} satisfies Partial<Components>

/** One interleaved text slice, rendered as its own bubble — see the
 * splitMessageParts call site in MessageItem for why a message can now
 * render more than one of these. */
function MessageBubble({ text, isUser }: { text: string; isUser: boolean }) {
  return (
    <div
      className={cn(
        'rounded-xl px-4 py-3 text-sm leading-relaxed',
        isUser
          ? 'bg-[var(--color-surface-2)] text-[var(--color-secondary)] rounded-tr-sm'
          : 'bg-transparent text-[var(--color-secondary)] rounded-tl-sm'
      )}
    >
      {isUser ? (
        <p className="whitespace-pre-wrap break-words">{text}</p>
      ) : (
        <div className="prose-sm prose-invert max-w-none">
          <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
            {text}
          </ReactMarkdown>
        </div>
      )}
    </div>
  )
}

// toLocaleTimeString() never throws on a malformed/unparseable timestamp — it
// simply renders the string "Invalid Date".
function formatTimestamp(ts: string): string {
  return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

// Avatar background/text color: accent-tinted for the user, the agent's own
// color when known, else a neutral surface fallback.
function avatarStyle(isUser: boolean, agentColor: string | undefined): React.CSSProperties {
  if (isUser) {
    return {
      backgroundColor: 'color-mix(in srgb, var(--color-accent) 20%, transparent)',
      color: 'var(--color-accent)',
    }
  }
  if (agentColor) {
    return { backgroundColor: agentColor, color: 'var(--color-secondary)' }
  }
  return { backgroundColor: 'var(--color-surface-3)', color: 'var(--color-secondary)' }
}

export function MessageItem({ message }: MessageItemProps) {
  const { toolCalls } = useChatStore()
  // ADR-051 — read once unconditionally at the top (Rules of Hooks: this
  // component takes no other conditionally-skipped hooks below). The
  // disclosure is mounted ONLY when verbose is on AND the message carries
  // typed error fields — see the errorDisclosure block below the footer.
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  const isUser = message.role === 'user'
  const isSystem = message.role === 'system'

  // Look up agent for assistant messages that carry an agentId.
  // Uses the cached ['agents'] query (prefetched by AppShell) — no extra network request.
  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 30_000,
  })
  const agent = message.agentId ? agents.find((a) => a.id === message.agentId) : null
  const agentName = agent?.name ?? (message.agentId ? message.agentId : null)

  if (isSystem) {
    return (
      <div className="flex justify-center py-2">
        <span className="text-xs text-[var(--color-muted)] bg-[var(--color-surface-2)] px-3 py-1 rounded-full">
          {message.content}
        </span>
      </div>
    )
  }

  // Interleave text and tool calls in the order they actually happened
  // (bug fix — see src/lib/messageParts.ts's doc comment): previously this
  // rendered the ENTIRE `message.content` as one bubble, then ALL tool
  // calls after it as a separate badge list, always in that order
  // regardless of when each call actually started relative to the text.
  // splitMessageParts uses each baked call's `textOffset` (stamped by the
  // store — PositionedToolCall in src/store/chat.ts) to rebuild the true
  // order; calls with no offset (legacy bakes / reconnect edge) fall back
  // to the old "after all text" placement.
  const positionedToolCalls = (message.tool_calls ?? []) as PositionedToolCall[]
  const messageParts = splitMessageParts(message.content ?? '', positionedToolCalls)
  // Live cursor for streaming: a message being actively streamed with no
  // text yet shows the "Thinking…" indicator instead of an (empty) bubble.
  // splitMessageParts never emits an empty text part, so this is checked
  // independently of `messageParts` rather than by inspecting its output.
  const showThinking = !!message.isStreaming && message.content === ''

  return (
    <div
      data-message-id={message.id}
      className={cn('group flex gap-3 px-4 py-3', isUser && 'flex-row-reverse')}
    >
      {/* Avatar — O11 icon attribution: for assistant messages, use the
          agent's own icon + color when known, rather than a generic Robot.
          This fixes the "icon misattribution" finding where every assistant
          message showed the same Robot regardless of which agent spoke. */}
      <div
        className={cn('shrink-0 w-7 h-7 rounded-full flex items-center justify-center text-xs')}
        style={avatarStyle(isUser, agent?.color)}
      >
        {isUser ? (
          <User size={14} weight="bold" />
        ) : agent?.icon ? (
          <IconRenderer icon={agent.icon} size={13} />
        ) : (
          <Robot size={14} weight="bold" />
        )}
      </div>

      {/* Content */}
      <div className={cn('flex flex-col gap-1 max-w-[85%] min-w-0', isUser && 'items-end')}>
        {/* Agent label — shown above assistant messages when the agent is known */}
        {!isUser && agentName && (
          <span
            data-testid="agent-label"
            className="text-[10px] font-medium text-[var(--color-accent)] px-1 capitalize"
          >
            {agentName}
          </span>
        )}
        {/* Streaming-with-no-text-yet indicator. Independent of the
            interleaved parts below (splitMessageParts never emits an empty
            text part) — matches the old behavior where "Thinking…" could
            show above already-baked tool call badges. */}
        {showThinking && (
          <div className="rounded-xl px-4 py-3 text-sm leading-relaxed bg-transparent text-[var(--color-secondary)] rounded-tl-sm">
            <span className="text-[var(--color-muted)] italic flex items-center gap-2">
              <span className="inline-block w-2 h-2 rounded-full bg-[var(--color-accent)] animate-pulse" />
              Thinking...
            </span>
          </div>
        )}

        {/* Text + tool calls, INTERLEAVED via splitMessageParts (bug fix):
            this used to render the WHOLE message bubble first, then ALL
            tool-call badges after it — collapsing whatever interleaved
            order the assistant actually streamed in once the message
            finished. Each text slice now renders as its own bubble and
            each tool call as its own badge, alternating in true stream
            order — see splitMessageParts's doc comment for the exact
            offset/fallback semantics. A tool call is only rendered if the
            live store still has a resolvable (call_id-bearing) entry for
            it — ToolCallBadge requires that shape; this mirrors the
            pre-existing lookup-miss guard (`.filter(Boolean)`), unchanged
            by this fix. */}
        {messageParts.map((part, idx) => {
          if (part.type === 'text') {
            return <MessageBubble key={`text-${idx}`} text={part.text} isUser={isUser} />
          }
          const resolved = toolCalls[part.call.id]
          if (!resolved) return null
          return (
            <div key={`tool-${part.call.id}`} className="w-full">
              <ToolCallBadge toolCall={resolved} />
            </div>
          )
        })}

        {/* Footer */}
        <div className="flex items-center gap-3 px-1">
          <span className="text-[10px] text-[var(--color-muted)]">
            {formatTimestamp(message.timestamp)}
          </span>
          {message.status === 'interrupted' && (
            <span className="text-[10px] text-[var(--color-muted)] italic">(interrupted)</span>
          )}
          {message.status === 'error' && (
            <span className="text-[10px] text-[var(--color-error)]">Error</span>
          )}
          {message.role === 'assistant' && <ModelFooter model={message.model} />}
        </div>

        {/* ADR-051 — verbose-only "Technical details" disclosure for typed
            LLM errors. Mounted ONLY when all four conditions hold: the
            bubble is in error status, a typed errorCode was stamped on it
            (live ErrorFrame with payload.llm_error), verbose chat is on,
            AND a non-empty errorDetail was carried (legacy frames and replay
            frames — which strip detail before persisting — never mount this).
            When verbose is off the disclosure is ABSENT from the DOM, not
            merely hidden — the user cannot open what isn't there, and the
            DOM stays clean for screen readers. Native <details> keeps this
            zero-dependency and keyboard-accessible out of the box. */}
        {message.status === 'error'
          && message.errorCode
          && verboseChatEnabled
          && message.errorDetail
          && message.errorDetail.length > 0 && (
          <details className="px-1 mt-1 group/error-detail" data-testid="error-detail-disclosure">
            <summary
              tabIndex={0}
              className={cn(
                'text-[10px] text-[var(--color-muted)] cursor-pointer select-none',
                'inline-flex items-center gap-1 hover:text-[var(--color-secondary)] transition-colors',
              )}
            >
              Technical details
            </summary>
            <pre
              className={cn(
                'mt-1 px-2 py-1.5 rounded-md',
                'bg-[var(--color-surface-2)] text-[var(--color-secondary)]',
                'font-mono text-[10px] leading-relaxed whitespace-pre-wrap break-all',
                'max-h-40 overflow-y-auto',
              )}
            >
              {message.errorDetail.slice(0, ERROR_DETAIL_MAX_CHARS)}
            </pre>
          </details>
        )}
      </div>
    </div>
  )
}
