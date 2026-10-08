/**
 * Above-feed kind label (FR-008, BDD-07.1).
 *
 * Main chat has no title suffix. An extra includes its title only when it
 * has one, with an em dash. Task and helper kinds pass through unchanged.
 */
import type { Session } from '@/lib/api'
import { isMainSession } from './sessionCoreSeam'

const EM_DASH = '—'

/** The fields the label reads. A full Session satisfies this; the workspace descriptor does too once a null title is coerced to ''. */
export interface FeedKindSource { // not-wire-format: SPA-only fields the above-feed kind label reads; a Session satisfies it, but this shape is never a request or response
  id: string
  type: Session['type']
  title: string
}

export function feedKindLabel(session: FeedKindSource): string {
  if (session.type === 'task') return 'Task run'
  if (session.type === 'delegate') return 'Helper'
  if (isMainSession(session)) return 'Main chat'
  const title = session.title.trim()
  if (title === '') return 'Extra chat'
  return `Extra chat ${EM_DASH} ${title}`
}
