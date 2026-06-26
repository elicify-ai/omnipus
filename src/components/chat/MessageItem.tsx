import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { User, Robot } from '@phosphor-icons/react'
import { useQuery } from '@tanstack/react-query'
import { ToolCallBadge } from './ToolCallBadge'
import { ModelFooter } from './ModelFooter'
import { IconRenderer } from '@/components/shared/IconRenderer'
import type { ChatMessage } from '@/store/chat'
import { useChatStore } from '@/store/chat'
import { fetchAgents } from '@/lib/api'
import { cn } from '@/lib/utils'

interface MessageItemProps {
  message: ChatMessage
}

// Wraps in try/catch because Date parsing can fail on malformed ISO strings
function formatTimestamp(ts: string): string {
  try {
    return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  } catch {
    return ''
  }
}

export function MessageItem({ message }: MessageItemProps) {
  const { toolCalls } = useChatStore()
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

  // Find tool calls that belong to this message
  const messageToolCalls = message.tool_calls?.map((tc) => toolCalls[tc.id]).filter(Boolean) ?? []

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
        style={
          isUser
            ? { backgroundColor: 'color-mix(in srgb, var(--color-accent) 20%, transparent)', color: 'var(--color-accent)' }
            : agent?.color
            ? { backgroundColor: agent.color, color: 'var(--color-secondary)' }
            : { backgroundColor: 'var(--color-surface-3)', color: 'var(--color-secondary)' }
        }
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
        {/* Message bubble */}
        <div
          className={cn(
            'rounded-xl px-4 py-3 text-sm leading-relaxed',
            isUser
              ? 'bg-[var(--color-surface-2)] text-[var(--color-secondary)] rounded-tr-sm'
              : 'bg-transparent text-[var(--color-secondary)] rounded-tl-sm'
          )}
        >
          {message.isStreaming && message.content === '' ? (
            <span className="text-[var(--color-muted)] italic flex items-center gap-2">
              <span className="inline-block w-2 h-2 rounded-full bg-[var(--color-accent)] animate-pulse" />
              Thinking...
            </span>
          ) : (
            <>
              {isUser ? (
                <p className="whitespace-pre-wrap break-words">{message.content}</p>
              ) : (
                <div className="prose-sm prose-invert max-w-none">
                  <ReactMarkdown
                    remarkPlugins={[remarkGfm]}
                    components={{
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
                        // Block executable URL schemes to prevent XSS via markdown
                        // links. javascript: runs script; data: can encode an HTML
                        // payload with embedded <script>; vbscript: is legacy IE
                        // but still flagged by CodeQL js/unsafe-jquery-plugin and
                        // similar rules. Whitespace prefixes are stripped before
                        // the comparison because browsers tolerate them in URLs.
                        const trimmed = href?.trim().toLowerCase() ?? ''
                        if (
                          trimmed.startsWith('javascript:') ||
                          trimmed.startsWith('data:') ||
                          trimmed.startsWith('vbscript:')
                        ) {
                          return <span>{children}</span>
                        }
                        return (
                          <a
                            href={href}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="text-[var(--color-accent)] underline underline-offset-2 hover:text-[var(--color-accent-hover)]"
                          >
                            {children}
                          </a>
                        )
                      },
                    }}
                  >
                    {message.content}
                  </ReactMarkdown>

                </div>
              )}
            </>
          )}
        </div>

        {/* Tool call badges (for assistant messages) */}
        {messageToolCalls.length > 0 && (
          <div className="w-full space-y-1">
            {messageToolCalls.map((tc) => (
              <ToolCallBadge key={tc.call_id} toolCall={tc} />
            ))}
          </div>
        )}

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
      </div>
    </div>
  )
}
