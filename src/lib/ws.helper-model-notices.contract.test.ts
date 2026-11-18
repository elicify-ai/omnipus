// #1537 order 4. Oracle: spec wire contract / FR-001/002/003/012.
// Raw JSON fixtures intentionally compile before the optional fields exist.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ProviderRetryFrame, ProviderStatusFrame } from './api/generated/ws-schemas'
import { parseFrameSafe } from './ws'
import { oldRetry, oldStatus } from '../../tests/helper-model-notices/old-provider-schemas'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'
import { inFlightReattachSids, gapReattachRetryTimers, gapReattachRetryAttempts } from '@/store/chat/runtime-state'

const at = '2026-11-15T12:00:00.000Z'
const retry = { type: 'provider_retry', session_id: 'top', turn_id: 'turn', provider: 'openrouter', model: 'model-a', sent_at: at,
  retry_at: '2026-11-15T12:00:10.000Z', retry_after_seconds: 10, attempt: 2, max_attempts: 6, error_code: 'rate_limited', seq: 6 }
const waiting = { type: 'provider_status', session_id: 'top', turn_id: 'turn', state: 'waiting', sent_at: at, silent_since: at, seq: 6 }
const identity = { child_session_id: 'helper-chat', agent_id: 'researcher' }
const cases = [
  { name: 'retry', schema: ProviderRetryFrame, old: oldRetry, frame: retry },
  ...['waiting', 'responding', 'recovered'].map(state => ({ name: state, schema: ProviderStatusFrame, old: oldStatus,
    frame: { ...waiting, state, ...(state === 'recovered' ? { retries: 1 } : {}) } })),
]

beforeEach(() => {
  inFlightReattachSids.clear()
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {}, messageOrder: [] } as never)
  useSessionStore.setState({ activeSessionId: 'top' })
  useConnectionStore.setState({ connection: null, connectionError: null })
})
afterEach(() => {
  for (const sid of Object.keys(gapReattachRetryTimers)) { clearTimeout(gapReattachRetryTimers[sid]); delete gapReattachRetryTimers[sid] }
  for (const sid of Object.keys(gapReattachRetryAttempts)) delete gapReattachRetryAttempts[sid]
  inFlightReattachSids.clear()
  // Dismiss real diagnostic toasts through their public cleanup so their
  // auto-dismiss timers cannot leak beyond the completed assertions.
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  vi.restoreAllMocks()
})

it.each(cases)('own $name remains accepted, byte-shape unchanged (regression control)', ({ schema, old, frame }) => {
  expect(schema.safeParse(frame)).toMatchObject({ success: true, data: frame })
  expect(old.safeParse(frame)).toMatchObject({ success: true, data: frame })
  expect(parseFrameSafe(frame)).toEqual(frame)
})

it.each(cases)('named $name copy validates without stripping either identity field', ({ schema, frame }) => {
  const named = { ...frame, ...identity }
  expect(schema.safeParse(named), 'FR-002: generated specific schema accepts paired identities').toMatchObject({ success: true, data: named })
  expect(parseFrameSafe(named), 'runtime union parser must accept exactly the same copy').toEqual(named)
})

it('ended with both child fields validates as a sequenced top-chat clear', () => {
  const frame = { type: 'provider_status', session_id: 'top', turn_id: 'turn', state: 'ended', sent_at: at, seq: 7, ...identity }
  expect(ProviderStatusFrame.safeParse(frame)).toMatchObject({ success: true, data: frame })
  expect(parseFrameSafe(frame)).toEqual(frame)
})

it.each(cases.flatMap(item => ['child_session_id', 'agent_id'].map(field => ({ ...item, field }))))('$name rejects a lone $field', ({ schema, frame, field }) => {
  const invalid = { ...frame, [field]: identity[field as keyof typeof identity] }
  expect(schema.safeParse(invalid).success, 'both-or-neither identity contract').toBe(false)
  expect(parseFrameSafe(invalid)).toBeNull()
})

it('ended without child fields is not a legal own-chat state', () => {
  const invalid = { ...waiting, state: 'ended' }
  expect(ProviderStatusFrame.safeParse(invalid).success).toBe(false)
  expect(parseFrameSafe(invalid)).toBeNull()
})

it.each(['child_session_id', 'agent_id'].flatMap(field => [0, 1, 2, 254, 255, 256].flatMap(length =>
  [cases[0], cases[1]].map(item => ({ ...item, field, length })))))('$name $field length $length obeys 1..255', ({ schema, frame, field, length }) => {
  const input = { ...frame, ...identity, [field]: 'x'.repeat(length) }
  const result = schema.safeParse(input)
  const valid = length >= 1 && length <= 255
  expect(result.success, 'spec optional identity string boundaries').toBe(valid)
  if (!valid && !result.success) {
    expect(result.error.issues).toEqual(expect.arrayContaining([expect.objectContaining({
      path: [field], code: length === 0 ? 'too_small' : 'too_big',
      ...(length === 0 ? { minimum: 1 } : { maximum: 255 }),
    })]))
  }
})

it.each(cases)('$name remains strict about unknown keys', ({ schema, frame }) => {
  const result = schema.safeParse({ ...frame, made_up_notice_field: true })
  expect(result.success).toBe(false)
  if (!result.success) expect(result.error.issues).toEqual(expect.arrayContaining([
    expect.objectContaining({ code: 'unrecognized_keys', keys: ['made_up_notice_field'] }),
  ]))
})

it.each(cases)('frozen old $name schema drops the named copy, then the real gap gate attaches only once', ({ old, frame }) => {
  const sent: unknown[] = []
  const own = { ...retry, seq: 5 }
  // Use the frozen old validator, not a mock of the current generated parser.
  const first = oldRetry.safeParse(own)
  expect(first.success, 'instrument: the old tab accepts a normal own retry').toBe(true)
  if (first.success) useChatStore.getState().handleFrame(first.data)
  const before = structuredClone(useChatStore.getState().sessionsById.top.providerRetryEvent)
  useConnectionStore.setState({ connection: { send: (value: unknown) => { sent.push(value); return true } } as never })
  const named = old.safeParse({ ...frame, ...identity })
  expect(named.success).toBe(false)
  // seq 6 was dropped. A subsequent otherwise-valid live frame reveals that
  // gap; the existing guarded recovery is the old-tab BDD compatibility path.
  for (const seq of [7, 8]) {
    const next = oldRetry.safeParse({ ...retry, seq })
    expect(next.success).toBe(true)
    if (next.success) useChatStore.getState().handleFrame(next.data)
  }
  expect(sent).toEqual([{ type: 'attach_session', session_id: 'top', since_seq: 5, boot_id: '' }])
  expect(useChatStore.getState().sessionsById.top.providerRetryEvent).toEqual(before)
  expect(useChatStore.getState().sessionsById.top.messageOrder).toEqual([])
  expect(useConnectionStore.getState().connectionError, 'old-tab gap recovery produces no user-facing connection error').toBeNull()
  expect(useChatStore.getState().messages).toEqual([])
})
