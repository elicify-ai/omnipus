/**
 * New-chat guard (FR-005, BDD-03.1, BDD-E02).
 *
 * An unconfirmed first send with no real session id prompts. Declining keeps
 * the original text and client message id. `__pending` is never the main and
 * never a saved pointer. Confirming starts an extra without replacing the main.
 */

type PendingStatus = 'sending' | 'unconfirmed' | 'retrying' | 'not_saved' | 'check_failed' | 'saved'

export type GuardInput = {
  mainSessionId: string
  activeSessionId: string | null
  pending: {
    sessionId: string | null
    clientMessageId: string
    text: string
    status: PendingStatus
  } | null
  choice?: 'decline' | 'confirm'
}

export type GuardResult =
  | { action: 'start-extra'; mainSessionId: string; replacedMain: false }
  | { action: 'prompt'; mainSessionId: string; clientMessageId: string; started: false }
  | {
      action: 'decline'
      mainSessionId: string
      activeSessionId: '__pending'
      savedPointer: null
      retainedClientMessageId: string
      retainedText: string
    }
  | { action: 'confirm'; mainSessionId: string; replacedMain: false; abandonedClientMessageId: string }

const UNCONFIRMED = new Set<PendingStatus>([
  'unconfirmed',
  'sending',
  'retrying',
  'not_saved',
  'check_failed',
])

function isUnconfirmedWithoutId(pending: GuardInput['pending']): pending is NonNullable<GuardInput['pending']> {
  return pending != null && pending.sessionId == null && UNCONFIRMED.has(pending.status)
}

export function decideNewChat(input: GuardInput): GuardResult {
  const { mainSessionId, pending, choice } = input
  if (isUnconfirmedWithoutId(pending) && choice === 'decline') {
    return {
      action: 'decline',
      mainSessionId,
      activeSessionId: '__pending',
      savedPointer: null,
      retainedClientMessageId: pending.clientMessageId,
      retainedText: pending.text,
    }
  }
  if (isUnconfirmedWithoutId(pending) && choice === 'confirm') {
    return {
      action: 'confirm',
      mainSessionId,
      replacedMain: false,
      abandonedClientMessageId: pending.clientMessageId,
    }
  }
  if (isUnconfirmedWithoutId(pending)) {
    return {
      action: 'prompt',
      mainSessionId,
      clientMessageId: pending.clientMessageId,
      started: false,
    }
  }
  return { action: 'start-extra', mainSessionId, replacedMain: false }
}
