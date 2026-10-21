// Inline reply status for the chat feed (W1-7). Replaces the bouncing-dots
// thinking indicator. The mark is the published AgentIcon; phrases are the
// ones the dots used to rotate. The icon does not fetch an agent.
import { useEffect, useState } from 'react'

import '@/styles/agent-icon-motion.css'

import { AgentIcon } from '@/components/ui/agent-icon'
import { WordBoundaryText } from '@/components/ui/word-boundary-text'
import { useCurrentTodo } from './useCurrentTodo'
import type { AgentColor, AgentFigure, AgentRole } from '@/lib/api/generated/openapi-types'
import { AGENT_ICON_PALETTE } from '@/design-system/agent-identity'
import { FIGURE_ART, ROLE_VOCABULARY } from '@/lib/agentIdentity'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useToolApprovalStore } from '@/store/toolApproval'

/** Existing thinking phrases. The first beat is always "Thinking…". */
export const THINKING_PHRASES = [
  'Thinking…',
  'Working on it…',
  'Composing a response…',
  'Processing your request…',
  'Analyzing…',
  'Considering the details…',
  'Piecing it together…',
  'Reasoning it through…',
  'Working through this…',
  'Gathering my thoughts…',
  'Figuring out the approach…',
  'Reviewing the context…',
  'Drafting a response…',
  'Making sense of it…',
  'Weighing the options…',
] as const

export const UNAVAILABLE_STATUS_TEXT = 'Unavailable/reconnecting'

// The kit's governed identity ink (founder 2026-10-11, AVATAR-PALETTE-
// DECISION.md) — one list, not a second copy. This is the avatar palette
// behind isPaletteColor, not a D4 task-status palette.
//
// Two compile-time guards pin it to the generated AgentColor enum (S3-07:
// the coverage direction below used to read the WIDENED annotation, which
// made `Exclude` always `never` and the guard unable to fail):
//   - subset — every palette entry IS an AgentColor: the annotation on the
//     assignment fails to typecheck for a stray entry;
//   - coverage — every AgentColor appears in the palette: the Exclude reads
//     the CONST's literal union, so a color added to the enum without a
//     palette entry leaves a non-never remainder and fails typecheck.
const IDENTITY_INK: readonly AgentColor[] = AGENT_ICON_PALETTE

type MissingPalette = Exclude<AgentColor, (typeof AGENT_ICON_PALETTE)[number]>
type AssertNever<T extends never> = T
type _PaletteCoverage = AssertNever<MissingPalette>
void (null as unknown as _PaletteCoverage)

export type ReplyPhase =
  | { kind: 'none' }
  | { kind: 'unavailable' }
  | { kind: 'waiting'; text: string }
  | { kind: 'working'; text: string }
  | { kind: 'thinking'; text: string | null }

type MarkPhase = 'thinking' | 'working' | 'waiting' | 'unavailable'

export function showsFeedMark(phase: ReplyPhase): phase is Extract<ReplyPhase, { kind: MarkPhase }> {
  return phase.kind === 'thinking' || phase.kind === 'working' || phase.kind === 'waiting' || phase.kind === 'unavailable'
}

export function feedMarkLabel(phase: ReplyPhase): string | null {
  if (phase.kind === 'thinking') return phase.text
  if (phase.kind === 'working' || phase.kind === 'waiting') return phase.text
  if (phase.kind === 'unavailable') return UNAVAILABLE_STATUS_TEXT
  return null
}

/** Next phrase, never the one just shown. Same rule the dots used. */
export function pickNextThinkingPhrase(current: string): string {
  if (THINKING_PHRASES.length <= 1) return THINKING_PHRASES[0]
  let next: string = current
  while (next === current) {
    next = THINKING_PHRASES[Math.floor(Math.random() * THINKING_PHRASES.length)]
  }
  return next
}

function readReducedMotion(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

function usePrefersReducedMotion(): boolean {
  const [reduced, setReduced] = useState(readReducedMotion)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const query = window.matchMedia('(prefers-reduced-motion: reduce)')
    const onChange = () => setReduced(query.matches)
    setReduced(query.matches)
    query.addEventListener?.('change', onChange)
    return () => query.removeEventListener?.('change', onChange)
  }, [])
  return reduced
}

function isFigure(value: string | undefined): value is AgentFigure {
  return value != null && Object.prototype.hasOwnProperty.call(FIGURE_ART, value)
}

function isRole(value: string | undefined): value is AgentRole {
  return value != null && ROLE_VOCABULARY.some((row) => row.slug === value)
}

function isPaletteColor(value: string | undefined): value is AgentColor {
  return value != null && (IDENTITY_INK as readonly string[]).includes(value)
}

function waitingCopy(approvalTool: string | null, questionPending: boolean): string {
  if (approvalTool && questionPending) return 'Waiting for your input'
  if (approvalTool) return `Waiting for your approval — ${approvalTool}`
  return 'Waiting for your input'
}

/**
 * Precedence follows the spec indicator table, limited to inputs that exist
 * today: disconnect, a pending approval, a pending question card, a correlated
 * running tool, a model turn — a settled, connected row paints nothing
 * ('none'). Goal/tool labels only choose the copy. Queued work has no
 * accepted input on this screen and is not invented here.
 */
export function useReplySlotPhase(input: {
  streaming: boolean
  /** A running tool correlated to this reply, independent of card visibility. */
  hasRunningTool: boolean
  toolLabel: string | null
  goalLabel: string | null
  /** Last settled assistant row. Idle is not painted on older rows. */
  idleEligible: boolean
}): ReplyPhase {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const pendingAsk = useChatStore((s) => s.pendingAsk)
  const approvalTool = useToolApprovalStore((s) => {
    if (!activeSessionId) return null
    const pending = s.queue.find((item) => item.sessionId === activeSessionId)
    return pending?.toolName ?? null
  })
  const linkDown = useConnectionStore((s) => {
    if (s.reconnectPhase === 'reconnecting' || s.reconnectPhase === 'slow' || s.reconnectPhase === 'gave_up') {
      return true
    }
    if (s.connectionError) return true
    return !s.isConnected && input.streaming
  })

  if (!input.streaming && !input.idleEligible) return { kind: 'none' }
  if (linkDown) return { kind: 'unavailable' }

  const questionPending =
    pendingAsk?.status === 'pending' &&
    (pendingAsk.session_id === activeSessionId || pendingAsk.session_id === '')
  if (approvalTool || questionPending) {
    return { kind: 'waiting', text: waitingCopy(approvalTool, questionPending) }
  }

  // Execution chooses the phase; goal copy still wins over the hidden-tool
  // label. Without specific copy, keep the existing working phrase stable.
  if (input.streaming && input.hasRunningTool) {
    return { kind: 'working', text: input.goalLabel ?? input.toolLabel ?? 'Working on it…' }
  }
  if (input.streaming) return { kind: 'thinking', text: input.goalLabel }
  // A settled, replay-free, connected row paints nothing: the "Idle" word is
  // retired (N3 — no production render used it; 'idle' and 'none' were
  // indistinguishable downstream).
  return { kind: 'none' }
}

function motionFor(phase: MarkPhase, reduced: boolean): 'none' | 'thinking' | 'working' | 'waiting' {
  if (reduced || phase === 'unavailable') return 'none'
  if (phase === 'working') return 'working'
  if (phase === 'waiting') return 'waiting'
  return 'thinking'
}

function ThinkingPhrase({ rotating }: { rotating: boolean }) {
  const [phrase, setPhrase] = useState<string>(THINKING_PHRASES[0])
  useEffect(() => {
    if (!rotating) return
    const interval = setInterval(() => setPhrase((previous) => pickNextThinkingPhrase(previous)), 2000)
    return () => clearInterval(interval)
  }, [rotating])
  return (
    <>
      {rotating && <span className="sr-only">Thinking</span>}
      <span aria-hidden={rotating ? true : undefined} className="text-[length:var(--type-utility-xs-size)]">{phrase}</span>
    </>
  )
}

/**
 * Figure, role badge, and the status phrase, inline in the feed. Changing
 * checklist/rotating copy is visual only; assistive text stays calm and stable.
 */
export function AgentStatusIndicator({
  phase,
  label,
  figure,
  role,
  color,
  name,
}: {
  phase: MarkPhase
  label: string | null
  figure?: string
  role?: string
  color?: string
  /** The agent's display name. Monogram draws its initial from this. */
  name?: string
}) {
  const reduced = usePrefersReducedMotion()
  // Connection/approval/question copy keeps its existing priority over work copy.
  const todo = useCurrentTodo(phase === 'thinking' || phase === 'working')
  const itemLabel = todo.unavailable ? 'Current checklist unavailable' : todo.text ? `Working on: ${todo.text}` : null
  const drawnFigure = isFigure(figure) ? figure : null
  const drawnRole = isRole(role) ? role : null
  const drawnColor = isPaletteColor(color) ? color : null
  const motion = motionFor(phase, reduced)
  // Monogram draws its initial from the name; with no name the mark is not drawn
  // (the existing incomplete-identity pattern — the phrase still renders). A
  // placeholder name is never substituted: it would draw a wrong initial.
  const nameAvailable = drawnFigure !== 'Monogram' || (name ?? '').trim().length > 0

  return (
    <span
      data-testid="reply-status-indicator"
      className="inline-flex min-w-0 max-w-full items-center gap-[var(--space-2)] py-[var(--space-1)] text-[var(--color-muted)] italic"
    >
      {drawnFigure && drawnRole && drawnColor && nameAvailable && (
        <span className="shrink-0" data-art={FIGURE_ART[drawnFigure].art}>
          <AgentIcon
            figure={drawnFigure}
            role={drawnRole}
            color={drawnColor}
            size={48}
            motion={motion}
            decorative
            name={name ?? ''}
            {...(reduced ? { reducedMotion: true as const } : {})}
          />
        </span>
      )}
      {todo.text && <span className="sr-only">Working on a checklist item</span>}
      {itemLabel ? (
        <span data-testid={todo.text ? 'reply-current-todo' : 'reply-current-todo-error'} aria-hidden={todo.text ? true : undefined} className="min-w-0 flex-1">
          <WordBoundaryText text={itemLabel} title={itemLabel} className="truncate text-[length:var(--type-utility-xs-size)]" />
        </span>
      ) : label != null ? (
        <span className="text-[length:var(--type-utility-xs-size)]">{label}</span>
      ) : (
        <ThinkingPhrase rotating={phase === 'thinking' && !reduced} />
      )}
    </span>
  )
}
