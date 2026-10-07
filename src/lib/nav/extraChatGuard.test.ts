/**
 * T-03 new-chat guard. Oracles: FR-005, BDD-03.1, BDD-E02, dataset N09.
 * The pair's main stays intact. An unconfirmed first send with no real
 * session id prompts; declining keeps the original text and client message
 * id. `__pending` is never the main and never a saved pointer.
 */
import { describe, expect, it } from 'vitest'

const SPEC = '@/lib/nav/extraChatGuard'

type PendingStatus = 'sending' | 'unconfirmed' | 'retrying' | 'not_saved' | 'check_failed' | 'saved'

type GuardInput = {
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

type GuardResult =
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

const MAIN = 'seam-opaque-main'
const ORIGINAL = 'Ship the launch notes'

async function expectGuard(input: GuardInput, expected: GuardResult, specRef: string): Promise<void> {
  let actual: GuardResult
  try {
    const mod = await import(/* @vite-ignore */ SPEC) as {
      decideNewChat: (value: GuardInput) => GuardResult
    }
    actual = mod.decideNewChat(input)
  } catch (err) {
    expect.fail(
      `BLOCKED: ${SPEC} decideNewChat not implemented — required by ${specRef}. Expected ${JSON.stringify(expected)}. Actual: module missing (${err instanceof Error ? err.message : String(err)})`,
    )
  }
  expect(actual).toEqual(expected)
  expect(JSON.stringify(actual)).not.toContain('"mainSessionId":"__pending"')
}

describe('decideNewChat (T-03, N09)', () => {
  it('BDD-03.1 with no unconfirmed delivery starts an extra and leaves the main intact', async () => {
    await expectGuard({
      mainSessionId: MAIN,
      activeSessionId: MAIN,
      pending: null,
    }, { action: 'start-extra', mainSessionId: MAIN, replacedMain: false }, 'FR-005, BDD-03.1')
  })

  it.each(['unconfirmed', 'sending', 'retrying', 'not_saved', 'check_failed'] as const)(
    'BDD-03.1 a %s first send with no real id prompts and does not start the extra',
    async (status) => {
      await expectGuard({
        mainSessionId: MAIN,
        activeSessionId: '__pending',
        pending: { sessionId: null, clientMessageId: 'client-1', text: ORIGINAL, status },
      }, {
        action: 'prompt',
        mainSessionId: MAIN,
        clientMessageId: 'client-1',
        started: false,
      }, 'FR-005, BDD-03.1, dataset N09')
    },
  )

  it('N09 / BDD-E02 declining keeps the original text and does not save the placeholder', async () => {
    await expectGuard({
      mainSessionId: MAIN,
      activeSessionId: '__pending',
      choice: 'decline',
      pending: { sessionId: null, clientMessageId: 'client-1', text: ORIGINAL, status: 'unconfirmed' },
    }, {
      action: 'decline',
      mainSessionId: MAIN,
      activeSessionId: '__pending',
      savedPointer: null,
      retainedClientMessageId: 'client-1',
      retainedText: ORIGINAL,
    }, 'BDD-E02, dataset N09')
  })

  it('confirming the guard starts the extra, abandons that delivery id, and does not replace the main', async () => {
    await expectGuard({
      mainSessionId: MAIN,
      activeSessionId: '__pending',
      choice: 'confirm',
      pending: { sessionId: null, clientMessageId: 'client-1', text: ORIGINAL, status: 'unconfirmed' },
    }, {
      action: 'confirm',
      mainSessionId: MAIN,
      replacedMain: false,
      abandonedClientMessageId: 'client-1',
    }, 'FR-005, BDD-03.1 main intact')
  })

  it('a pending delivery that already has a real session id does not prompt', async () => {
    await expectGuard({
      mainSessionId: MAIN,
      activeSessionId: 'real-session',
      pending: { sessionId: 'real-session', clientMessageId: 'client-1', text: ORIGINAL, status: 'unconfirmed' },
    }, { action: 'start-extra', mainSessionId: MAIN, replacedMain: false }, 'dataset N09 the guard is only for no real id')
  })

  it('a saved first send is not the unconfirmed-delivery guard', async () => {
    await expectGuard({
      mainSessionId: MAIN,
      activeSessionId: '__pending',
      pending: { sessionId: null, clientMessageId: 'client-1', text: ORIGINAL, status: 'saved' },
    }, { action: 'start-extra', mainSessionId: MAIN, replacedMain: false }, 'BDD-03.1 the guard is the unconfirmed first delivery')
  })
})
