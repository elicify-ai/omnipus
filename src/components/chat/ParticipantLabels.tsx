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
import type { AgentIdentity } from '@/components/agents/AgentMark'
import { fetchAgents, fetchWorkspaces } from '@/lib/api'
import type { Agent } from '@/lib/api'
import type { ChatMessage } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { ReplyHeader, TruncatedLabel } from './participantLabel'
import { collectPairedReplyToIds, participantInitial, participantLabel, replyHeaderParts, truncateParticipantLabel } from './participantLabel'

/**
 * F17 (1) — the thread's paired reply_to ids, memoized per messages array.
 * The list rows get theirs from the parent lists (identity-stable memo);
 * MessageItem — which already re-renders on every store change via its
 * whole-store subscription — takes this cheap per-render memo instead.
 * Accepts undefined: a consumer without a messages projection (narrow
 * store shapes) degrades to an empty set — no pairing, today's rendering —
 * never a crash.
 */
export function usePairedReplyToIds(messages: readonly { replyToMessageId?: string }[] | undefined): ReadonlySet<string> {
  return useMemo(() => collectPairedReplyToIds(messages ?? []), [messages])
}

/** Everything a label/header needs from the catalog + current workspace.
 * The agents carry their identity fields (figure/role/color) too: the catalog
 * query returns the full Agent records, and the reply label's AgentMark and
 * the inline indicator read those fields off the same lookup. */
export interface ParticipantLabelParts {
  agents: Array<{ id: string; name: string } & AgentIdentity>
  workspaces: Array<Pick<Agent, 'id' | 'name'>>
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
 * Founder UI feedback 2026-10-11 (point 1) — the agent reply label: the
 * agent's small AgentMark (the same component the sidebar and the @ menu use,
 * size 18 — the menu's small size) beside the name, or beside the U8 reply
 * header ("Ava → Alice · Telegram") on a reply bubble. ONE component for
 * every reply render path — MessageItem.tsx and ChatScreen.tsx's live row and
 * VirtualAssistantMessageRow (plain + virtualized) — so the surfaces cannot
 * drift.
 *
 * The label carries NO status word: the founder reads the old inline
 * "Idle"/"Unavailable" phrasing next to the name as a stray word, so the
 * label is the mark + the name (header) alone. Status phrasing belongs to the
 * inline indicator (AgentStatusIndicator) and the connection surfaces.
 *
 * The name span stays plain text and CHILDLESS — the mark is a SIBLING of
 * `agent-label`, never inside it, so the label span's textContent is exactly
 * the name (the wave1 name-only contract on the label span itself) and
 * copy/paste reads the name alone.
 */
export function AgentReplyLabel({ agent, name, header }: {
  agent: AgentIdentity | undefined
  name: string
  header: ReplyHeader | null
}) {
  return (
    <span className="inline-flex min-w-0 items-center gap-[var(--space-2)]">
      <span data-testid="agent-label-avatar" className="shrink-0">
        <AgentMark agent={agent} name={name} size={18} />
      </span>
      {header ? (
        <ReplyHeaderView header={header} />
      ) : (
        <span
          data-testid="agent-label"
          title={name || undefined}
          className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
        >
          {name}
        </span>
      )}
    </span>
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
            {/* C9: the FIRST GRAPHEME, not the first UTF-16 unit — an emoji
                or combined first character comes out whole instead of as a
                lone surrogate (a blank glyph). */}
            {participantInitial(participant.display_name)}
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
