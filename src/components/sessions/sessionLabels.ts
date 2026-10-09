// Session row words for the Sessions view (Wave 1, FR-025/FR-026/FR-036).
// Labels come from lifecycle_state, execution, and type. Coarse `status`
// never becomes Working: `active` covers working, waiting, and stopped.

import type { Session } from '@/lib/api'

export type SessionStatusTone =
  | 'working'
  | 'waiting'
  | 'queued'
  | 'done'
  | 'failed'
  | 'stopped'
  | 'interrupted'
  | 'unavailable'

export interface SessionStatusLabel {
  text: string
  tone: SessionStatusTone
}

const STOP_CAUSE_WORDS: Record<NonNullable<Session['stop_note']>['cause'], string> = {
  stop: 'stop',
  redirect_pause: 'redirect pause',
  cascade: 'cascade',
  restart: 'restart',
  timeout: 'timeout',
}

/** Queued wins over Working. A missing lifecycle is Unavailable, except archived → Done. */
export function sessionStatusLabel(session: Session): SessionStatusLabel {
  if (session.execution === 'queued') return { text: 'Queued', tone: 'queued' }
  switch (session.lifecycle_state) {
    case 'working':
      return { text: 'Working', tone: 'working' }
    case 'waiting_for_answer':
      return { text: 'Waiting', tone: 'waiting' }
    case 'done':
      return { text: 'Done', tone: 'done' }
    case 'failed':
      return { text: 'Failed', tone: 'failed' }
    case 'stopped':
      return { text: stoppedText(session), tone: 'stopped' }
    case 'interrupted':
      return { text: 'Interrupted', tone: 'interrupted' }
    default:
      if (session.status === 'archived') return { text: 'Done', tone: 'done' }
      return { text: 'Unavailable', tone: 'unavailable' }
  }
}

function stoppedText(session: Session): string {
  const cause = session.stop_note?.cause
  if (!cause) return 'Stopped'
  return `Stopped · ${STOP_CAUSE_WORDS[cause]}`
}

/** Real kind. delegate is Helper. Never the old "HB" abbreviation. */
export function sessionKindLabel(type: Session['type']): string {
  switch (type) {
    case 'delegate':
      return 'Helper'
    case 'channel':
      return 'Channel'
    case 'heartbeat':
      return 'Heartbeat'
    case 'chat':
      return 'Chat'
    case 'task':
      return 'Task'
    case 'scheduled':
      return 'Scheduled'
    case 'verifier':
      return 'Verifier'
    default: {
      const unexpected: never = type
      return unexpected
    }
  }
}

/**
 * Origin-row shell sentence. Undefined (unknown) and 0 (checked, none) draw
 * nothing. Not a roll-up of child sessions — the caller passes this session's
 * own count.
 */
export function backgroundCommandSentence(count: number | undefined): string | null {
  if (count === undefined || count <= 0) return null
  const noun = count === 1 ? 'command' : 'commands'
  return `${count} background ${noun} running`
}
