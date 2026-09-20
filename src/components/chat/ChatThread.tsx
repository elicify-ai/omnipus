// ChatThread — standalone chat thread component for testing and embedding.
// Production ChatScreen wraps this with the full shell (input, session bar).
import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useChatStore } from '@/store/chat'
import { MessageItem } from './MessageItem'
import { fetchSessionMessages } from '@/lib/api'

interface ChatThreadProps {
  /** Load and display messages for this session */
  sessionId?: string
}

export function ChatThread({ sessionId }: ChatThreadProps) {
  const { messages, setMessages } = useChatStore()

  const { data: historyData, isError } = useQuery({
    queryKey: ['messages', sessionId],
    queryFn: () => fetchSessionMessages(sessionId!),
    enabled: !!sessionId,
  })

  useEffect(() => {
    if (historyData) setMessages(historyData)
  }, [historyData, setMessages])

  if (isError) {
    return (
      <div className="flex justify-center py-[var(--space-3)] text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">
        Could not load messages.
      </div>
    )
  }

  return (
    <div role="log" aria-label="Chat messages">
      {messages.map((msg) => (
        <MessageItem key={msg.id} message={msg} />
      ))}
    </div>
  )
}
