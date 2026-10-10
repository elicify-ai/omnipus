/**
 * Shown-commit acknowledgement through the production path (FR-047; spec
 * C-ATTENTION rows ~187-191, BDD-13.2/13.3).
 *
 * The REAL chat store reduces a generated SessionStateFrame — the contract
 * carries attention_bound on the attach response's SessionStateFrame for
 * BOTH the incremental catch-up and the full snapshot — then the REAL
 * catch_up_complete handler asks the acknowledgement, and the outgoing
 * attach is captured from the real connection. The seam (sessionCoreSeam /
 * mainAttention) is NOT mocked.
 *
 * Oracles (docs/internal/specs/session-core-spec.md; AttachSessionFrame.yaml):
 * a shown main acknowledges with exactly { ack_attention: true,
 * attention_bound: <server integer> }; no bound, a non-main, a background
 * prefetch, an overtaken (reconnect) attach ⇒ no ack fields; a newer bound
 * seen before a retry must never change the retry's captured bound.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from '@/store/chat/store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import type { Session } from '@/lib/api'
import type { ServerFrame, WsConnection } from '@/lib/ws'
import { noteForegroundAttach } from './foregroundAck'

/** Real loaded session metadata as the sidebar's ['sessions'] query holds it. */
function mainSession(id: string, overrides: Partial<Session> = {}): Session {
  return {
    id,
    agent_id: 'mia',
    title: 'Mia main',
    type: 'main',
    needs_attention: true,
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-10T00:00:00Z',
    message_count: 1,
    workspace_id: 'ws-1',
    ...overrides,
  }
}

function chatSession(id: string): Session {
  return {
    id,
    agent_id: 'mia',
    title: 'An extra chat',
    type: 'chat',
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-10T00:00:00Z',
    message_count: 1,
  }
}

/** Generated SessionStateFrame attach answer; the bound is optional per the contract. */
function stateFrame(sid: string, attentionBound?: number): ServerFrame {
  return {
    type: 'session_state',
    user_id: '',
    pending_approvals: [],
    emitted_at: '2026-10-10T00:00:00Z',
    session_id: sid,
    ...(attentionBound === undefined ? {} : { attention_bound: attentionBound }),
  } as unknown as ServerFrame
}

function catchUpComplete(sid: string): ServerFrame {
  return {
    type: 'catch_up_complete',
    session_id: sid,
    seq: 1,
    boot_id: 'boot-t',
    mode: 'incremental',
  } as unknown as ServerFrame
}

let sent: unknown[]
let hiddenSpy: ReturnType<typeof vi.spyOn>

function connectionThat(delivers: boolean): WsConnection {
  return {
    send: (frame: unknown) => {
      if (delivers) sent.push(frame)
      return delivers
    },
  } as unknown as WsConnection
}

/** Production setup: active session, chat store, connection, loaded roster metadata, visible tab, foreground attach. */
function shownForeground(sid: string, loaded: Session[]): void {
  useSessionStore.setState({ activeSessionId: sid })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  useConnectionStore.setState({ connection: connectionThat(true) } as never)
  // An empty list means the roster has NOT resolved — leave the cache cold so
  // the cold-cache / deferred-acknowledgement scenarios start honest.
  if (loaded.length > 0) queryClient.setQueryData(['sessions'], loaded)
  hiddenSpy.mockReturnValue(false)
  noteForegroundAttach(sid)
}

function snapshotFrame(sid: string): ServerFrame {
  return {
    type: 'session_snapshot',
    session_id: sid,
    seq: 3,
    boot_id: 'boot-s',
    reason: 'unknown_position',
  } as unknown as ServerFrame
}

beforeEach(() => {
  sent = []
  hiddenSpy = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
})

afterEach(() => {
  hiddenSpy.mockRestore()
  queryClient.removeQueries({ queryKey: ['sessions'] })
  useConnectionStore.setState({ connection: null } as never)
  vi.restoreAllMocks()
})

describe('shown-commit acknowledgement through the production path (seam unmocked)', () => {
  it('a shown main acknowledges with exactly { ack_attention: true, attention_bound: 7 } from the attach response SessionStateFrame', () => {
    const sid = 'main-ack-positive'
    shownForeground(sid, [mainSession(sid)])

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })

  it('no bound on the attach response ⇒ nothing is sent (no ack fields, never a substituted number)', () => {
    const sid = 'main-ack-no-bound'
    shownForeground(sid, [mainSession(sid)])

    useChatStore.getState().handleFrame(stateFrame(sid))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([])
  })

  it('a bound of 9 arriving before the retry never changes the captured bound of 7 (never recapture)', () => {
    const sid = 'main-ack-retry'
    shownForeground(sid, [mainSession(sid)])
    // First attempt fails to deliver: the fields are captured, not acked.
    useConnectionStore.setState({ connection: connectionThat(false) } as never)

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([])

    // The connection recovers and a NEWER bound arrives before the retry.
    useConnectionStore.setState({ connection: connectionThat(true) } as never)
    useChatStore.getState().handleFrame(stateFrame(sid, 9))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })

  it('a non-main session is never acknowledged', () => {
    const sid = 'extra-chat-no-ack'
    shownForeground(sid, [chatSession(sid)])

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([])
  })

  it('a background (hidden-tab) attach is a prefetch and never acknowledges', () => {
    const sid = 'main-ack-background'
    shownForeground(sid, [mainSession(sid)])
    hiddenSpy.mockReturnValue(true)

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([])
  })

  it('an attach overtaken by another active session (reconnect) never acknowledges', () => {
    const sid = 'main-ack-overtaken'
    shownForeground(sid, [mainSession(sid)])
    useSessionStore.setState({ activeSessionId: 'some-other-session' })

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([])
  })
})

describe('shown-commit acknowledgement on a cold roster cache (round 2 HIGH: the ack must not be lost)', () => {
  it('a direct open whose validated session-detail is loaded acknowledges exactly once with the captured bound', () => {
    const sid = 'main-ack-cold-detail'
    shownForeground(sid, []) // the sidebar roster has NOT resolved
    queryClient.setQueryData(['session-detail', sid], { session: mainSession(sid), messages: [] })

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })

  it('metadata still unknown at completion defers: the ack sends when the roster resolves, with the ORIGINAL bound — a newer frame never replaces it', () => {
    const sid = 'main-ack-defer'
    shownForeground(sid, []) // nothing loaded at attach time

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([]) // unknown metadata is never acknowledged on the spot

    // A newer bound arrives before the metadata does — it must not be captured.
    useChatStore.getState().handleFrame(stateFrame(sid, 9))

    queryClient.setQueryData(['sessions'], [mainSession(sid)])
    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })

  it('a deferred ack is dropped when the session is no longer the active foreground chat when the metadata resolves', () => {
    const sid = 'main-ack-defer-moved-on'
    shownForeground(sid, [])
    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    useSessionStore.setState({ activeSessionId: 'a-different-chat' })
    queryClient.setQueryData(['sessions'], [mainSession(sid)])

    expect(sent).toEqual([])
  })

  it('a deferred ack is dropped when another attach has replaced the shown foreground commit', () => {
    const sid = 'main-ack-defer-replaced'
    shownForeground(sid, [])
    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    noteForegroundAttach('a-newer-attach')
    queryClient.setQueryData(['sessions'], [mainSession(sid)])

    expect(sent).toEqual([])
  })

  it('metadata that resolves with UNKNOWN attention never produces ack fields (R2-T2)', () => {
    const sid = 'main-ack-unknown-attention'
    shownForeground(sid, [mainSession(sid, { needs_attention: undefined })])

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([])

    // A later cache event must not send either.
    queryClient.setQueryData(['sessions'], [mainSession(sid, { needs_attention: undefined })])
    expect(sent).toEqual([])
  })

  it('full-snapshot attach answer: snapshot without a bound, then session_state with 7, then snapshot-mode completion ⇒ exactly one ack with 7 (R2-T1)', () => {
    const sid = 'main-ack-full-snapshot'
    shownForeground(sid, [mainSession(sid)])

    useChatStore.getState().handleFrame(snapshotFrame(sid))
    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame({
      type: 'catch_up_complete',
      session_id: sid,
      seq: 4,
      boot_id: 'boot-s',
      mode: 'snapshot',
    } as unknown as ServerFrame)

    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })
})

describe('frozen pending record (round 4 invariants, spec FR-047 / BDD-13.2 / no-write row ~L191)', () => {
  it('I2: a reconnect completion carrying bound 9 never replaces the deferred 7 — resolution acks the original 7', () => {
    const sid = 'main-ack-i2-reconnect'
    shownForeground(sid, []) // metadata unknown at the shown commit

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    // A reconnect (no new foreground attach) completes again with a newer bound.
    useChatStore.getState().handleFrame(stateFrame(sid, 9))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    queryClient.setQueryData(['sessions'], [mainSession(sid)])
    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })

  it('I4: a failed deferred send keeps the record — after recovery and a newer completion, exactly one ack carries the original 7', () => {
    const sid = 'main-ack-i4-failed-deferred-send'
    shownForeground(sid, [])
    useConnectionStore.setState({ connection: connectionThat(false) } as never)

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    queryClient.setQueryData(['sessions'], [mainSession(sid)]) // metadata resolves; send fails
    expect(sent).toEqual([])

    // Connection recovers; a newer completion arrives before any retry.
    useConnectionStore.setState({ connection: connectionThat(true) } as never)
    useChatStore.getState().handleFrame(stateFrame(sid, 9))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })

  it('I3: a retry after the user opened a different chat sends nothing', () => {
    const sid = 'main-ack-i3-retry-after-switch'
    shownForeground(sid, [mainSession(sid)])
    useConnectionStore.setState({ connection: connectionThat(false) } as never)

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([]) // first attempt failed; the record is kept (I4)

    useConnectionStore.setState({ connection: connectionThat(true) } as never)
    useSessionStore.setState({ activeSessionId: 'a-newer-chat' }) // user moved on
    useChatStore.getState().handleFrame(catchUpComplete(sid)) // retry attempt

    expect(sent).toEqual([])
  })

  it('I3: a retry while still the foreground chat sends the original 7 exactly once', () => {
    const sid = 'main-ack-i3-retry-in-front'
    shownForeground(sid, [mainSession(sid)])
    useConnectionStore.setState({ connection: connectionThat(false) } as never)

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([])

    useConnectionStore.setState({ connection: connectionThat(true) } as never)
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })

  it('I5: a populated roster that omits the target session is not borrowed — no ack fields', () => {
    const sid = 'main-ack-i5-not-listed'
    shownForeground(sid, [mainSession('another-main-entirely')])

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([])

    // Later cache events must not send either while the target stays unlisted.
    queryClient.setQueryData(['sessions'], [mainSession('another-main-entirely')])
    expect(sent).toEqual([])
  })

  it('I5: unknown attention arriving through the deferred resolver sends no ack fields', () => {
    const sid = 'main-ack-i5-deferred-unknown-attention'
    shownForeground(sid, []) // defer first: metadata unknown

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    // Metadata resolves with UNKNOWN attention — still no ack fields.
    queryClient.setQueryData(['sessions'], [mainSession(sid, { needs_attention: undefined })])
    expect(sent).toEqual([])

    // And a further refresh that still leaves attention unknown sends nothing.
    queryClient.setQueryData(['sessions'], [mainSession(sid, { needs_attention: undefined })])
    expect(sent).toEqual([])
  })
})

describe('final round (R1–R3): the record belongs to exactly one shown open', () => {
  it('R1: a completion without a number closes the record — a later reconnect never fills it; a fresh open acks its own number', () => {
    const sid = 'main-ack-r1-boundless'
    shownForeground(sid, [mainSession(sid)])

    // The shown open completes without any bound on its attach answer.
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([])

    // A reconnect in the SAME generation supplies 9 — the closed record must not take it.
    useChatStore.getState().handleFrame(stateFrame(sid, 9))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([])

    // A fresh foreground open is a new record: it acknowledges its OWN number.
    noteForegroundAttach(sid)
    useChatStore.getState().handleFrame(stateFrame(sid, 12))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 12 },
    ])
  })

  it('R2: metadata resolving after the user moved on drops the record — returning to the chat does not resurrect it without a new open', () => {
    const sid = 'main-ack-r2-dropped'
    shownForeground(sid, [])

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    // The user opens a different chat (no new attach for sid), then metadata resolves.
    useSessionStore.setState({ activeSessionId: 'another-chat' })
    queryClient.setQueryData(['sessions'], [mainSession(sid)])
    expect(sent).toEqual([])

    // Returning to the old chat (without a new shown open) must not revive the record.
    useSessionStore.setState({ activeSessionId: sid })
    queryClient.setQueryData(['sessions'], [mainSession(sid)])
    expect(sent).toEqual([])
  })

  it('R3: a stale completion from an older open never touches the current record — the reopened open acks its own 9 exactly once', () => {
    const sid = 'main-ack-r3-stale-completion'
    shownForeground(sid, [mainSession(sid)])

    // Open 1: its answer carries 7 and it completes — acknowledged once.
    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])

    // The user goes elsewhere, then reopens the SAME session (a new record).
    noteForegroundAttach('chat-b')
    noteForegroundAttach(sid)

    // A STALE completion from open 1 arrives before the reopened answer does.
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])

    // The reopened open's own answer and completion acknowledge 9 exactly once.
    useChatStore.getState().handleFrame(stateFrame(sid, 9))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 9 },
    ])
  })
})
