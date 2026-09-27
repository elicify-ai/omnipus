// session.attach-cursor.test.ts: BE-DESIGN.md §6.1 — attachToSession sends
// {since_seq, boot_id} from the bucket's own cursor (src/store/chat.ts's
// registerGetSessionCursor wiring), rather than wiping the bucket and
// replaying from scratch on every (re)attach. See
// session.workspace.test.ts's rewritten "does NOT reset the chat bucket"
// test for the companion no-wipe invariant and its full Q3 provenance.

import { describe, it, expect, beforeEach } from 'vitest'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useWorkspacesStore } from './workspacesStore'
import { useChatStore } from './chat/store'
import { emptySessionState } from './chat/session'
// Importing chat.ts runs its module-level registerGetSessionCursor(...)
// side effect, wiring session.ts's attachToSession to the real chat store.
import '@/store/chat'

function makeMockConnection() {
  return { send: (() => true) as unknown as (frame: unknown) => boolean, close: () => {}, isConnected: true }
}

describe('attachToSession sends the bucket cursor (§6.1)', () => {
  beforeEach(() => {
    useSessionStore.setState({ activeSessionId: null, sessionByWorkspace: {}, resolvingSessionForWorkspace: {} })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useChatStore.setState({ sessionsById: {} } as never)
  })

  it('sends since_seq/boot_id when the session bucket already has a cursor', () => {
    useChatStore.setState({
      sessionsById: {
        'sess-with-cursor': { ...emptySessionState(), cursor: { bootId: 'boot-Z', seq: 77 } },
      },
    } as never)
    let sentFrame: unknown = null
    const conn = { send: (f: unknown) => { sentFrame = f; return true }, close: () => {}, isConnected: true }
    useConnectionStore.setState({ connection: conn as never, isConnected: true })

    useSessionStore.getState().attachToSession('sess-with-cursor', 'chat', 'Title', 'agent-1')

    expect(sentFrame).toEqual({
      type: 'attach_session',
      session_id: 'sess-with-cursor',
      since_seq: 77,
      boot_id: 'boot-Z',
    })
  })

  it('sends the bare frame (no since_seq/boot_id) when there is no cursor yet', () => {
    let sentFrame: unknown = null
    const conn = { send: (f: unknown) => { sentFrame = f; return true }, close: () => {}, isConnected: true }
    useConnectionStore.setState({ connection: conn as never, isConnected: true })

    useSessionStore.getState().attachToSession('sess-fresh', 'chat', 'Title', 'agent-1')

    expect(sentFrame).toEqual({ type: 'attach_session', session_id: 'sess-fresh' })
  })

  it('does NOT wipe the existing bucket on attach — its messages survive', () => {
    useChatStore.setState({
      sessionsById: {
        'sess-with-history': {
          ...emptySessionState(),
          messagesById: { m1: { id: 'm1', role: 'assistant', content: 'hi', timestamp: 't', status: 'done' } },
          messageOrder: ['m1'],
        },
      },
    } as never)
    useConnectionStore.setState({ connection: makeMockConnection() as never, isConnected: true })

    useSessionStore.getState().attachToSession('sess-with-history', 'chat', 'Title', 'agent-1')

    const bucket = useChatStore.getState().sessionsById['sess-with-history']
    expect(bucket.messageOrder).toEqual(['m1'])
    expect(bucket.messagesById.m1?.content).toBe('hi')
  })
})
