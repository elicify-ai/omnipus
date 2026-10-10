// ParticipantLabel.tsx — U8 participant label components (F15/F17, D4).
//
// The label/header logic itself lives in participantLabel.ts (pure helpers);
// every row renderer — MessageItem.tsx (live/standalone) and ChatScreen.tsx's
// VirtualUserMessageRow / VirtualAssistantMessageRow (plain + virtualized
// history) — calls THESE components/helpers so the surfaces cannot drift.
//
// Render contract (ARCHITECT-ANSWER-PARTICIPANT.md §D4 + founder F17):
//   - a user-role entry WITH a participant is a LEFT-side message carrying
//     the label (the backend stamps participants only on server-written
//     entries — channel participants and peer requests — so the web user's
//     own bubbles never reach here; F17: own = right, no label);
//   - a reply bubble (reply_to_message_id / reply_to_participant present)
//     renders "author → recipient" instead of the plain author label;
//   - display_name is PLAIN TEXT, ellipsized at ~40 chars, full text in the
//     catalogued Tooltip (never markdown/HTML — the name is channel input).

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Tooltip } from '@/components/ui/tooltip'
import { AgentMark } from '@/components/agents/AgentMark'
import { fetchAgents, fetchWorkspaces } from '@/lib/api'
import type { ChatMessage } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { TruncatedLabel } from './participantLabel'
import { collectPairedReplyToIds, participantLabel, replyHeaderParts, truncateParticipantLabel } from './participantLabel'

/**
 * F17 (1) — the thread's paired reply_to ids, memoized per messages array.
 * The list rows get theirs from the parent lists (identity-stable memo);
 * MessageItem — which already re-renders on every store change via its
 * whole-store subscription — takes this cheap per-render memo instead.
 */
export function usePairedReplyToIds(messages: readonly { replyToMessageId?: string }[]): ReadonlySet<string> {
  return useMemo(() => collectPairedReplyToIds(messages), [messages])
}

/** Everything a label/header needs from the catalog + current workspace. */
export interface ParticipantLabelParts {
  agents: Array<{ id: string; name: string }>
  workspaces: Array<{ id: string; name: string }>
  currentWorkspaceId: string | null
}

/**
 * The cached catalog lookups the labels resolve against (same query keys the
 * rows already use for agents — cache hits are free; prefetched by AppShell).
 */
export function useParticipantLabelParts(): ParticipantLabelParts {
  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 30_000,
  })
  const { data: workspaces = [] } = useQuery({
    queryKey: ['workspaces'],
    // Wrapped: react-query calls queryFn with a context object, and
    // fetchWorkspaces's optional params bag must not receive it.
    queryFn: () => fetchWorkspaces(),
    staleTime: 30_000,
  })
  const currentWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  return { agents, workspaces, currentWorkspaceId }
}

const LABEL_CLASS =
  'text-[length:var(--type-caption-size)] text-[var(--color-muted)] min-w-0 max-w-full truncate'

/**
 * One plain-text label span — ellipsized at the cap with the full text in the
 * catalogued Tooltip only when actually truncated (an untruncated label never
 * grows a tooltip that would only repeat it).
 */
export function ParticipantNameLabel({ label, testId }: { label: TruncatedLabel; testId?: string }) {
  const span = (
    <span data-testid={testId ?? 'participant-label'} className={LABEL_CLASS}>
      {label.text}
    </span>
  )
  if (!label.truncated) return span
  return (
    <Tooltip content={label.full} side="top" data-testid="participant-label-tooltip">
      {span}
    </Tooltip>
  )
}

/**
 * D4 rows 5-6 — the "Ava → Alice · Telegram" header of a reply bubble. The
 * target degrades to "reply" when only reply_to_message_id exists. Rendered
 * IN PLACE OF the plain author label (the header includes the author).
 */
export function ReplyHeaderView({ header }: { header: NonNullable<ReturnType<typeof replyHeaderParts>> }) {
  const target = (
    <span data-testid="reply-target" className="min-w-0 max-w-full truncate">
      {header.target}
    </span>
  )
  return (
    <span
      data-testid="reply-header"
      className="inline-flex max-w-full items-center text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
    >
      {/* The arrow is literal TEXT (not a flex-gap seam) so the header reads
          exactly as the ruling writes it — "Ava → Alice · Telegram" — in
          copy/paste, textContent and screen readers alike. */}
      <span className="shrink-0">{header.author} → </span>
      {header.targetTruncated ? (
        <Tooltip content={header.targetFull} side="top" data-testid="reply-target-tooltip">
          <span data-testid="reply-target-trigger" className="min-w-0 max-w-full truncate">
            {target}
          </span>
        </Tooltip>
      ) : (
        target
      )}
    </span>
  )
}

/**
 * D4 rows 1-2 — the LEFT-side row for a user-role entry that carries a
 * participant (bound-connector input, a peer request). Mirror of
 * ChatScreen's VirtualUserMessageRow: left-aligned, participant label above
 * the bubble, plain text body. These entries are server-written raw text —
 * no slash commands, no media, no delivery states, so the row is small by
 * construction.
 */
export function ParticipantMessageRow({ message }: { message: ChatMessage }) {
  const { agents, workspaces, currentWorkspaceId } = useParticipantLabelParts()
  const participant = message.participant
  if (!participant) return null
  const label = truncateParticipantLabel(
    participantLabel(participant, agents, currentWorkspaceId, workspaces),
  )
  const isAgentParticipant = participant.kind === 'agent'
  const agent = isAgentParticipant
    ? agents.find((a) => a.id === participant.agent?.agent_id)
    : undefined
  return (
    <div
      data-testid="participant-message"
      data-message-role="user"
      data-message-id={message.id}
      className="group flex gap-[var(--space-2-5)] px-[var(--space-3)] py-[var(--space-2-5)]"
    >
      {/* A guest agent draws its own mark; a human participant draws the
          neutral catalogued Avatar (initial fallback), never the accent USER
          avatar — that one means the web user's own messages. */}
      {isAgentParticipant ? (
        <AgentMark agent={agent} name={label.full} size={26} />
      ) : (
        <Avatar size="sm" className="shrink-0">
          <AvatarFallback className="bg-[var(--color-surface-2)] text-[var(--color-muted)]">
            {participant.display_name.charAt(0).toUpperCase()}
          </AvatarFallback>
        </Avatar>
      )}
      <div className="flex min-w-0 max-w-[85%] flex-col items-start gap-[var(--space-1)]">
        <ParticipantNameLabel label={label} />
        <div className="w-fit max-w-full rounded-xl rounded-tl-sm bg-[var(--color-surface-2)] px-[var(--space-3)] py-[var(--space-2-5)] text-[length:var(--type-body-compact-size)] leading-relaxed text-[var(--color-secondary)]">
          <p className="whitespace-pre-wrap break-words">{message.content}</p>
        </div>
      </div>
    </div>
  )
}
