// RoomTrafficEntry — group-chat traffic inside an agent's main chat
// (omnipus-system-agent FR-202, FR-215; K33; BDD-17.15).
//
// Both kinds of room traffic — the room's request to this agent and the
// agent's answer into the room — render as ONE compact, collapsed entry
// "From group <name>: <first line>" (FR-202 names that form for both). Expanding
// shows the whole text (the request with its room context, or the answer
// with its author) and a link to the group. The entry is display and
// navigation only: a RoomRef never grants access to the room.

import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import type { RoomRef } from '@/lib/api/generated/asyncapi-types'
import { fetchAgents } from '@/lib/api'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { ChatMessage } from '@/store/chat/types'

export interface RoomTrafficEntryProps {
  roomRef: RoomRef
  text: string
  /** The answering agent's display name (answers only). */
  agentName?: string
}

function firstLine(text: string): string {
  return text.split('\n').find((line) => line.trim().length > 0)?.trim() ?? ''
}

export function RoomTrafficEntry({ roomRef, text, agentName }: RoomTrafficEntryProps) {
  const [expanded, setExpanded] = useState(false)
  // A main chat and its groups share one workspace (FR-162): the open main's
  // workspace is the room's.
  const workspaceId = useWorkspacesStore((state) => state.activeWorkspaceId)
  return (
    <div
      data-testid="room-traffic-entry"
      className="mx-[var(--space-3)] my-[var(--space-1)] rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
    >
      <DisclosureRow
        data-testid="room-traffic-toggle"
        expandable
        expanded={expanded}
        onExpandedChange={setExpanded}
        className="w-full justify-start text-left"
      >
        <span className="min-w-0 truncate text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
          From group {roomRef.room_name}: {firstLine(text)}
        </span>
      </DisclosureRow>
      {expanded && (
        <div className="flex flex-col gap-[var(--space-1)] px-[var(--space-3)] pb-[var(--space-2)]">
          {roomRef.role === 'answer' && agentName && (
            <span className="text-[length:var(--type-caption-size)] font-semibold text-[var(--color-secondary)]">{agentName}</span>
          )}
          <p className="whitespace-pre-wrap text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">{text}</p>
          {workspaceId && (
            <Link
              tabIndex={0}
              to="/workspaces/$workspaceId/rooms/$roomId"
              params={{ workspaceId, roomId: roomRef.room_id }}
              className="self-start text-[length:var(--type-caption-size)] text-[var(--color-accent)] underline-offset-2 hover:underline"
            >
              Open group {roomRef.room_name}
            </Link>
          )}
        </div>
      )}
    </div>
  )
}

/** A main-chat message carrying a RoomRef, with its answering agent resolved by id. */
export function RoomTrafficMessage({ message }: { message: ChatMessage }) {
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, staleTime: 30_000 })
  if (!message.roomRef) return null
  const agentName = message.agentId ? agents.find((agent) => agent.id === message.agentId)?.name ?? message.agentId : undefined
  return <RoomTrafficEntry roomRef={message.roomRef} text={message.content} agentName={agentName} />
}
