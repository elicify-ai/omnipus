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
import { registerGetSessionCursor, useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import type { Session } from '@/lib/api'
import type { ServerFrame, WsConnection } from '@/lib/ws'
import type { SessionStateFrame } from '@/lib/api/generated/asyncapi-types'
import { noteForegroundAttach } from './foregroundAck'
import { availableHistoryDetail } from '../../../tests/chat-history-paging/legacy-fixtures'
import { expectValidFrame, expectValidPage, page, snapshot } from '../../../tests/chat-history-paging/fixtures'
import { loadFixtureHistory } from '../../../tests/chat-history-paging/load-fixture-history'

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
function stateFrame(sid: string, attentionBound?: number): SessionStateFrame {
  return {
    type: 'session_state',
    user_id: '',
    pending_approvals: [],
    emitted_at: '2026-10-10T00:00:00Z',
    session_id: sid,
    history_state: { status: 'available' },
    ...(attentionBound === undefined ? {} : { attention_bound: attentionBound }),
  }
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
async function shownForeground(sid: string, loaded: Session[]): Promise<void> {
  useSessionStore.setState({ activeSessionId: sid })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  useConnectionStore.setState({ connection: connectionThat(true) } as never)
  // An empty list means the roster has NOT resolved — leave the cache cold so
  // the cold-cache / deferred-acknowledgement scenarios start honest.
  if (loaded.length > 0) queryClient.setQueryData(['sessions'], loaded)
  hiddenSpy.mockReturnValue(false)
  // A prior validated page permits incremental catch-up. REST does not add
  // a fabricated attention answer to this NEW shown-open record.
  await loadFixtureHistory(useChatStore, sid)
  noteForegroundAttach(sid)
}

function snapshotFrame(sid: string): ServerFrame {
  const history = page([], { sessionId: sid })
  expectValidPage(history, [], 0)
  const frame: ServerFrame = { ...snapshot(history), seq: 4, boot_id: 'boot-s' }
  expectValidFrame(frame)
  return frame
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
  vi.unstubAllGlobals()
})

describe('shown-commit acknowledgement through the production path (seam unmocked)', () => {
  it('a shown main acknowledges with exactly { ack_attention: true, attention_bound: 7 } from the attach response SessionStateFrame', async () => {
    const sid = 'main-ack-positive'
    await shownForeground(sid, [mainSession(sid)])

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })

  it('no bound on the attach response ⇒ nothing is sent (no ack fields, never a substituted number)', async () => {
    const sid = 'main-ack-no-bound'
    await shownForeground(sid, [mainSession(sid)])

    useChatStore.getState().handleFrame(stateFrame(sid))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([])
  })

  it('a bound of 9 arriving before the retry never changes the captured bound of 7 (never recapture)', async () => {
    const sid = 'main-ack-retry'
    await shownForeground(sid, [mainSession(sid)])
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

  it('a non-main session is never acknowledged', async () => {
    const sid = 'extra-chat-no-ack'
    await shownForeground(sid, [chatSession(sid)])

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([])
  })

  it('a background (hidden-tab) attach is a prefetch and never acknowledges', async () => {
    const sid = 'main-ack-background'
    await shownForeground(sid, [mainSession(sid)])
    hiddenSpy.mockReturnValue(true)

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([])
  })

  it('an attach overtaken by another active session (reconnect) never acknowledges', async () => {
    const sid = 'main-ack-overtaken'
    await shownForeground(sid, [mainSession(sid)])
    useSessionStore.setState({ activeSessionId: 'some-other-session' })

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([])
  })
})

describe('shown-commit acknowledgement on a cold roster cache (round 2 HIGH: the ack must not be lost)', () => {
  it('a direct open whose validated session-detail is loaded acknowledges exactly once with the captured bound', async () => {
    const sid = 'main-ack-cold-detail'
    await shownForeground(sid, []) // the sidebar roster has NOT resolved
    queryClient.setQueryData(['session-detail', sid], availableHistoryDetail(mainSession(sid)))

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })

  it('metadata still unknown at completion defers: the ack sends when the roster resolves, with the ORIGINAL bound — a newer frame never replaces it', async () => {
    const sid = 'main-ack-defer'
    await shownForeground(sid, []) // nothing loaded at attach time

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

  it('a deferred ack is dropped when the session is no longer the active foreground chat when the metadata resolves', async () => {
    const sid = 'main-ack-defer-moved-on'
    await shownForeground(sid, [])
    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    useSessionStore.setState({ activeSessionId: 'a-different-chat' })
    queryClient.setQueryData(['sessions'], [mainSession(sid)])

    expect(sent).toEqual([])
  })

  it('a deferred ack is dropped when another attach has replaced the shown foreground commit', async () => {
    const sid = 'main-ack-defer-replaced'
    await shownForeground(sid, [])
    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    noteForegroundAttach('a-newer-attach')
    queryClient.setQueryData(['sessions'], [mainSession(sid)])

    expect(sent).toEqual([])
  })

  it('metadata that resolves with UNKNOWN attention never produces ack fields (R2-T2)', async () => {
    const sid = 'main-ack-unknown-attention'
    await shownForeground(sid, [mainSession(sid, { needs_attention: undefined })])

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([])

    // A later cache event must not send either.
    queryClient.setQueryData(['sessions'], [mainSession(sid, { needs_attention: undefined })])
    expect(sent).toEqual([])
  })

  it('full-snapshot attach answer: snapshot without a bound, then session_state with 7, then snapshot-mode completion ⇒ exactly one ack with 7 (R2-T1)', async () => {
    const sid = 'main-ack-full-snapshot'
    await shownForeground(sid, [mainSession(sid)])

    useChatStore.getState().handleFrame(snapshotFrame(sid))
    const current: ServerFrame = { ...stateFrame(sid, 7), history_state: { status: 'available' } }
    expectValidFrame(current)
    useChatStore.getState().handleFrame(current)
    const replayDone: ServerFrame = { type: 'done', session_id: sid }
    expectValidFrame(replayDone)
    useChatStore.getState().handleFrame(replayDone)
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
  it('I2: a reconnect completion carrying bound 9 never replaces the deferred 7 — resolution acks the original 7', async () => {
    const sid = 'main-ack-i2-reconnect'
    await shownForeground(sid, []) // metadata unknown at the shown commit

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

  it('I4: a failed deferred send keeps the record — after recovery and a newer completion, exactly one ack carries the original 7', async () => {
    const sid = 'main-ack-i4-failed-deferred-send'
    await shownForeground(sid, [])
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

  it('I3: a retry after the user opened a different chat sends nothing', async () => {
    const sid = 'main-ack-i3-retry-after-switch'
    await shownForeground(sid, [mainSession(sid)])
    useConnectionStore.setState({ connection: connectionThat(false) } as never)

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([]) // first attempt failed; the record is kept (I4)

    useConnectionStore.setState({ connection: connectionThat(true) } as never)
    useSessionStore.setState({ activeSessionId: 'a-newer-chat' }) // user moved on
    useChatStore.getState().handleFrame(catchUpComplete(sid)) // retry attempt

    expect(sent).toEqual([])
  })

  it('I3: a retry while still the foreground chat sends the original 7 exactly once', async () => {
    const sid = 'main-ack-i3-retry-in-front'
    await shownForeground(sid, [mainSession(sid)])
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

  // M6 port (the former mainAttention copy's "hidden-reconnect" pin): a
  // reconnect completion while the tab is hidden is a prefetch and sends
  // nothing; the SAME record still sends its original bound once the tab is
  // visible again — the guard waits, it does not cancel.
  it('I3: a reconnect completion while hidden sends nothing; the same record sends its original 7 once visible', async () => {
    const sid = 'main-ack-i3-hidden-reconnect'
    await shownForeground(sid, [mainSession(sid)])
    useConnectionStore.setState({ connection: connectionThat(false) } as never)

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([])

    useConnectionStore.setState({ connection: connectionThat(true) } as never)
    hiddenSpy.mockReturnValue(true)
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent, 'hidden reconnect is a prefetch, never a send').toEqual([])

    hiddenSpy.mockReturnValue(false)
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent, 'the waiting record sends the original bound once visible').toEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })

  it('I5: a populated roster that omits the target session is not borrowed — no ack fields', async () => {
    const sid = 'main-ack-i5-not-listed'
    await shownForeground(sid, [mainSession('another-main-entirely')])

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))
    expect(sent).toEqual([])

    // Later cache events must not send either while the target stays unlisted.
    queryClient.setQueryData(['sessions'], [mainSession('another-main-entirely')])
    expect(sent).toEqual([])
  })

  it('I5: unknown attention arriving through the deferred resolver sends no ack fields', async () => {
    const sid = 'main-ack-i5-deferred-unknown-attention'
    await shownForeground(sid, []) // defer first: metadata unknown

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
  it('R1: a completion without a number closes the record — a later reconnect never fills it; a fresh open acks its own number', async () => {
    const sid = 'main-ack-r1-boundless'
    await shownForeground(sid, [mainSession(sid)])

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

  it('R2: metadata resolving after the user moved on drops the record — returning to the chat does not resurrect it without a new open', async () => {
    const sid = 'main-ack-r2-dropped'
    await shownForeground(sid, [])

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

  it('R3: a stale completion from an older open never touches the current record — the reopened open acks its own 9 exactly once', async () => {
    const sid = 'main-ack-r3-stale-completion'
    await shownForeground(sid, [mainSession(sid)])

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

// Delta R3 (browser verification, test gateway): sign-in sent TWO attach_session
// frames for the active chat, the second with ack_attention:true. The ack frame
// is intended, but it carried no cursor, so the gateway answered it with a
// full snapshot + transcript replay (cursorless attach = snapshot, A5-A6) - a
// second full replay of the whole chat for a one-integer acknowledgement.
// Sent with the bucket's own cursor it is answered incrementally.
describe('the acknowledging attach is cheap: it carries the bucket cursor', () => {
  // Production registers the getter from store/chat.ts; this file loads the
  // store module directly, so register the same shape here and undo it after.
  afterEach(() => registerGetSessionCursor(() => null))

  it('a shown main with a cursor acknowledges with { ack_attention, attention_bound } PLUS since_seq and boot_id', async () => {
    const sid = 'main-ack-cursor'
    registerGetSessionCursor((id) => (id === sid ? { bootId: 'boot-c', seq: 4 } : null))
    await shownForeground(sid, [mainSession(sid)])

    useChatStore.getState().handleFrame(stateFrame(sid, 7))
    useChatStore.getState().handleFrame(catchUpComplete(sid))

    expect(sent).toHaveLength(1)
    expect(sent[0]).toMatchObject({
      type: 'attach_session',
      session_id: sid,
      ack_attention: true,
      attention_bound: 7,
      since_seq: 4,
      boot_id: 'boot-c',
    })
  })
})

// r19 a/b/d negative controls. D7a: server history availability is not proof
// of a loaded client page. D10/D11: contradictory page metadata is an error,
// not successful empty history. All pre-existing cases/helpers stay intact.
function c2OpenWithoutHistory(sid: string, isMain = true): void {
  useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: sid }, true)
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState({ ...useConnectionStore.getInitialState(), connection: connectionThat(true), isConnected: true }, true)
  queryClient.setQueryData(['sessions'], [isMain ? mainSession(sid) : chatSession(sid)])
  hiddenSpy.mockReturnValue(false)
  useChatStore.getState().resetSessionForReplay(sid)
  noteForegroundAttach(sid)
  expect(useChatStore.getState().sessionsById[sid].history.historyLoaded,
    'instrument: no page was preloaded for this negative control').toBe(false)
  expect(useChatStore.getState().sessionsById[sid].replayCompletedForSession).toBeNull()
}

function c2FeedAvailable(sid: string): void {
  const frame = stateFrame(sid, 7)
  expectValidFrame(frame)
  useChatStore.getState().handleFrame(frame)
}

function c2CompleteLive(sid: string, cut: number): void {
  const frame: ServerFrame = { type: 'catch_up_complete', session_id: sid, seq: cut, boot_id: 'c2-negative-boot', mode: 'incremental' }
  expectValidFrame(frame)
  useChatStore.getState().handleFrame(frame)
}

function c2CaptureHistoryReads(sid: string, body: ReturnType<typeof page>): string[] {
  const reads: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(input instanceof Request ? input.url : String(input), 'http://localhost')
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    reads.push(`${method} ${url.pathname}${url.search}`)
    if (method !== 'GET' || url.pathname !== `/api/v1/sessions/${sid}/messages`) {
      throw new Error(`Undeclared C2 HTTP request: ${method} ${url.pathname}`)
    }
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }))
  return reads
}

// Each named case is the exact selector handed to the independent a/b/d
// probes. These current-implementation controls are not self-CHECK kills.
describe('C2 r19 missing-scenario negative controls through real history and acknowledgement owners', () => {
  it('r19-a: an available incremental completion never acknowledges an unloaded main; the real page permits exactly bound 7', async () => {
    const sid = 'c2-r19-a-unloaded-main'
    c2OpenWithoutHistory(sid)
    c2FeedAvailable(sid)
    expect(useChatStore.getState().sessionsById[sid].history.serverState).toStrictEqual({ status: 'available' })
    c2CompleteLive(sid, 8)

    expect(sent, 'D7/D7a: server availability plus COMPLETE is not a shown history page, so no seen write').toStrictEqual([])
    const valid = page([], { sessionId: sid })
    expectValidPage(valid, [], 0)
    const reads = c2CaptureHistoryReads(sid, valid)
    await useChatStore.getState().loadLatestHistory(sid)
    expect(reads).toStrictEqual([`GET /api/v1/sessions/${sid}/messages?limit=40&min_rows=20`])
    expect(useChatStore.getState().sessionsById[sid].history.historyLoaded).toBe(true)
    c2CompleteLive(sid, 9)

    expect(sent, 'positive control: an actual loaded page permits the same foreground open to acknowledge exactly its server bound').toStrictEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })

  it('r19-b: incremental COMPLETE commits live controls and cursor but cannot invent historyLoaded or replay completion', () => {
    const sid = 'c2-r19-b-unloaded-chat'
    c2OpenWithoutHistory(sid, false)
    c2FeedAvailable(sid)
    expect(useChatStore.getState().sessionsById[sid].isReplaying).toBe(true)
    c2CompleteLive(sid, 8)

    const state = useChatStore.getState().sessionsById[sid]
    expect(state.cursor, 'D7: successful live/control catch-up commits its exact W independently of historical proof').toStrictEqual({ bootId: 'c2-negative-boot', seq: 8 })
    expect(state.awaitingCatchUp).toBe(false)
    expect(state.isReplaying).toBe(false)
    expect(state.history.liveControlsReady).toBe(true)
    expect(state.history.serverState).toStrictEqual({ status: 'available' })
    expect(state.history.historyLoaded, 'D7a: COMPLETE cannot manufacture a page that was never delivered').toBe(false)
    expect(state.replayCompletedForSession, 'D7a: live readiness is not successful history reconstruction').toBeNull()
    expect(state.history.readerPositions).toStrictEqual([])
    expect(state.messageOrder).toStrictEqual([])
  })

  it('r19-d: contradictory HTTP page source count fails visibly without history or ack; valid explicit Retry recovers', async () => {
    const sid = 'c2-r19-d-bad-page-count'
    c2OpenWithoutHistory(sid)
    c2FeedAvailable(sid)
    const valid = page([], { sessionId: sid })
    expectValidPage(valid, [], 0)
    const contradictory = { ...valid, page_info: { ...valid.page_info, source_group_count: 1 } }
    const { SessionHistoryResponse: GeneratedPage } = await import('@/lib/api/generated/schemas')
    expect(GeneratedPage.safeParse(contradictory).success,
      'instrument: generated structure accepts this deliberate semantic contradiction; real production validation must still reject it').toBe(true)
    expect(contradictory.entries).toStrictEqual([])
    expect(contradictory.page_info.source_group_count).toBe(1)
    const rejectedReads = c2CaptureHistoryReads(sid, contradictory)

    await useChatStore.getState().loadLatestHistory(sid)
    const rejected = useChatStore.getState().sessionsById[sid]
    expect(rejectedReads).toStrictEqual([`GET /api/v1/sessions/${sid}/messages?limit=40&min_rows=20`])
    expect(rejected.history.requests.latest, 'D8/D10/D11: impossible page metadata is an action-owned visible latest failure').toStrictEqual({
      status: 'error', error: 'Could not load latest messages.', attempt: 1,
    })
    expect(rejected.history.historyLoaded).toBe(false)
    expect(rejected.history.readerPositions).toStrictEqual([])
    expect(rejected.messageOrder).toStrictEqual([])
    expect(rejected.replayCompletedForSession).toBeNull()
    c2CompleteLive(sid, 8)
    expect(sent, 'invalid history plus a valid live COMPLETE still cannot write the seen mark').toStrictEqual([])
    expect(useChatStore.getState().sessionsById[sid].history.requests.latest.error).toBe('Could not load latest messages.')

    const retryReads = c2CaptureHistoryReads(sid, valid)
    await useChatStore.getState().retryHistory(sid, 'latest')
    const recovered = useChatStore.getState().sessionsById[sid]
    expect(retryReads, 'Retry repeats only the bounded latest read').toStrictEqual([`GET /api/v1/sessions/${sid}/messages?limit=40&min_rows=20`])
    expect(recovered.history.requests.latest).toStrictEqual({ status: 'idle', error: null, attempt: 2 })
    expect(recovered.history.historyLoaded).toBe(true)
    c2CompleteLive(sid, 9)
    expect(sent).toStrictEqual([
      { type: 'attach_session', session_id: sid, ack_attention: true, attention_bound: 7 },
    ])
  })
})
