// OpenInChatButton — navigates to the session a task is/was running in,
// extracted from TaskDetailPanel so the calendar's recurring-task EDIT
// slide-over can offer the same link (recurring tasks are excluded from
// Board/List, US-3 — the calendar slide-over is their only surface, and
// currently the only place a recurring task's chat link was unreachable).
//
// Self-gates: renders nothing unless the resolved session id is set —
// identical gate to TaskDetailPanel's "Open in Chat" button (#250 regression
// coverage).
//
// Run-aware (ADR-050 RD8 / task-run-history-spec §4.3): an optional `run`
// prop lets the calendar's occurrence slide-over open THAT run's session
// instead of task.session_id — the task-level mirror only ever points at
// the LATEST session, which is wrong for an earlier occurrence's chat link.
// Absent → falls back to task.session_id (unchanged existing behaviour).

import { useNavigate } from '@tanstack/react-router'
import { Button } from '@/components/ui/button'
import { ChatCircle } from '@phosphor-icons/react'
import type { Task, TaskRun } from '@/lib/api'

export interface OpenInChatButtonProps {
  task: Task
  /** A specific occurrence's resolved run (ADR-050 RD8). See file header. */
  run?: TaskRun
  /** Called after navigation fires — e.g. close the host panel/slide-over. */
  onNavigate?: () => void
}

export function OpenInChatButton({ task, run, onNavigate }: OpenInChatButtonProps) {
  const navigate = useNavigate()

  const sessionId = run ? run.session_id : task.session_id

  if (!sessionId) return null

  return (
    <Button
      variant="outline"
      size="sm"
      className="w-full gap-2 text-xs h-8"
      onClick={() => {
        void navigate({ to: '/sessions/$sessionId', params: { sessionId } })
        onNavigate?.()
      }}
    >
      <ChatCircle size={13} />
      Open in Chat
    </Button>
  )
}
