// OpenInChatButton — navigates to the session a task is/was running in,
// extracted from TaskDetailPanel so the calendar's recurring-task EDIT
// slide-over can offer the same link (recurring tasks are excluded from
// Board/List, US-3 — the calendar slide-over is their only surface, and
// currently the only place a recurring task's chat link was unreachable).
//
// Self-gates: renders nothing unless task.session_id is set — identical
// gate to TaskDetailPanel's "Open in Chat" button (#250 regression coverage).

import { useNavigate } from '@tanstack/react-router'
import { Button } from '@/components/ui/button'
import { ChatCircle } from '@phosphor-icons/react'
import type { Task } from '@/lib/api'

export interface OpenInChatButtonProps {
  task: Task
  /** Called after navigation fires — e.g. close the host panel/slide-over. */
  onNavigate?: () => void
}

export function OpenInChatButton({ task, onNavigate }: OpenInChatButtonProps) {
  const navigate = useNavigate()

  if (!task.session_id) return null

  return (
    <Button
      variant="outline"
      size="sm"
      className="w-full gap-2 text-xs h-8"
      onClick={() => {
        void navigate({ to: '/sessions/$sessionId', params: { sessionId: task.session_id! } })
        onNavigate?.()
      }}
    >
      <ChatCircle size={13} />
      Open in Chat
    </Button>
  )
}
