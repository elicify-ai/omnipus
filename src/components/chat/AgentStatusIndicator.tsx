// Inline reply status for the chat feed. The mark is the published AgentIcon;
// work copy is an active form or one fixed fallback. The icon fetches nothing.
import { useEffect, useState } from 'react'

import '@/styles/agent-icon-motion.css'
import '@/styles/agent-status-motion.css'

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

/** One randomly selected fallback per status appearance, never a phrase clock. */
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

export function pickThinkingPhrase(): string {
  return THINKING_PHRASES[Math.floor(Math.random() * THINKING_PHRASES.length)]
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
 * ('none'). Work motion is independent of the active-form-or-fixed-fallback
 * copy selected below. Queued work has no accepted input on this screen.
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

  // Execution chooses motion; AgentStatusIndicator selects work copy from
  // the accepted active form or its one fixed fallback, not these labels.
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

/** Figure, role badge and visual work copy; assistive copy stays stable. */
export function AgentStatusIndicator({
  phase,
  label,
  figure,
  role,
  color,
  name,
  fallbackPhrase,
}: {
  phase: MarkPhase
  label: string | null
  figure?: string
  role?: string
  color?: string
  /** The agent's display name. Monogram draws its initial from this. */
  name?: string
  /** Shared across the live identity-loading Suspense boundary. */
  fallbackPhrase?: string
}) {
  const reduced = usePrefersReducedMotion()
  // Connection/approval/question copy keeps its existing priority over work copy.
  const workPhase = phase === 'thinking' || phase === 'working'
  const todo = useCurrentTodo(workPhase)
  const activeForm = workPhase ? todo.activeForm : null
  const [fallback] = useState(() => fallbackPhrase ?? pickThinkingPhrase())
  const drawnFigure = isFigure(figure) ? figure : null
  const drawnRole = isRole(role) ? role : null
  const drawnColor = isPaletteColor(color) ? color : null
  const motion = motionFor(phase, reduced)
  // Monogram draws its initial from the name; with no name the mark is not drawn
  // (the existing incomplete-identity pattern — the phrase still renders). A
  // placeholder name is never substituted: it would draw a wrong initial.
  const nameAvailable = drawnFigure !== 'Monogram' || (name ?? '').trim().length > 0
  const identity = drawnFigure && drawnRole && drawnColor && nameAvailable
    ? [drawnFigure, drawnRole, drawnColor, name ?? ''].join(':') : 'no-identity'
  // Both loops start in the same paint when motion/identity changes. Copy
  // updates preserve this epoch and therefore cannot restart either clock.
  const epoch = `${motion}:${identity}`

  return (
    <span
      data-testid="reply-status-indicator"
      data-agent-status-motion={motion}
      className="inline-flex min-w-0 max-w-full items-center gap-[var(--space-2)] py-[var(--space-1)] text-[var(--color-muted)] italic"
    >
      {drawnFigure && drawnRole && drawnColor && nameAvailable && (
        <span key={`icon-${epoch}`} className="shrink-0" data-art={FIGURE_ART[drawnFigure].art}>
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
      {workPhase && <span className="sr-only">{activeForm ? 'Working on a checklist item' : 'Thinking'}</span>}
      <span key={`text-${epoch}`} data-testid="agent-status-text" className="agent-status-text min-w-0 flex-1">
        {workPhase ? activeForm ? (
          <span data-testid="reply-current-todo" aria-hidden="true">
            <WordBoundaryText text={activeForm} title={activeForm} className="truncate text-[length:var(--type-utility-xs-size)]" />
          </span>
        ) : (
          <span data-testid="reply-status-fallback" aria-hidden="true" className="text-[length:var(--type-utility-xs-size)]">{fallback}</span>
        ) : (
          <span className="text-[length:var(--type-utility-xs-size)]">{label}</span>
        )}
      </span>
    </span>
  )
}
