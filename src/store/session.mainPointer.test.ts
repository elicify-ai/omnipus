/**
 * M2 final-gate finding — rememberMainPointer must remember through the
 * production path. The old code called isMainSession({ id: sessionId }) with
 * no type, so the check was always false and mainPointerByPair was never
 * written outside tests. These tests drive attachToSession — the only
 * production writer — with the REAL seam (sessionCoreSeam is not mocked) and
 * the session record's own type, never a pre-seeded pointer.
 *
 * Oracles: FR-005/BDD-03.1 — the main pointer per `${workspaceId}::${agentId}`
 * is the validated main; an extra never replaces it; `__pending` is never a
 * pointer (setActiveSession's own guard).
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'

const WS = 'operations'
const PAIR = `${WS}::mia`

function connect() {
  const send = vi.fn().mockReturnValue(true)
  useConnectionStore.setState({ connection: { send } as never, isConnected: true })
  return send
}

beforeEach(() => {
  localStorage.clear()
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useWorkspacesStore.setState({ activeWorkspaceId: WS })
})

describe('rememberMainPointer — the production attach path writes the pair pointer (M2)', () => {
  it('attaching the workspace main records it under workspace::owner', () => {
    connect()
    const attached = useSessionStore.getState().attachToSession('main-mia', 'main', 'Mia main', 'mia')
    expect(attached).toBe(true)
    expect(useSessionStore.getState().mainPointerByPair, 'the real main attach is remembered').toEqual({
      [PAIR]: 'main-mia',
    })
  })

  it('attaching an extra (type chat) never writes the pair pointer', () => {
    connect()
    const attached = useSessionStore.getState().attachToSession('extra-1', 'chat', 'An extra', 'mia')
    expect(attached).toBe(true)
    expect(useSessionStore.getState().mainPointerByPair, 'an extra is never a main pointer').toEqual({})
  })

  it('a later extra attach never steals the pair pointer the main wrote', () => {
    connect()
    useSessionStore.getState().attachToSession('main-mia', 'main', 'Mia main', 'mia')
    useSessionStore.getState().attachToSession('extra-1', 'chat', 'An extra', 'mia')
    expect(useSessionStore.getState().mainPointerByPair[PAIR], 'the main pointer stays the main').toBe('main-mia')
  })
})
