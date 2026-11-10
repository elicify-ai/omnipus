// D15 intentional contract migration: preserve the five carrier scenarios,
// replacing retired array/header/detail-optional authority with atomic pages.
// REAL request/validation/adapters; only global fetch/cookie are controlled.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  availableDetail, entry, expectValidDetail, expectValidPage, page, pos, session,
} from '../../../tests/chat-history-paging/fixtures'

function stubCookieLocal(value: string) {
  Object.defineProperty(document, 'cookie', { configurable: true, get: () => value })
}
function restoreCookieLocal() {
  Reflect.deleteProperty(document, 'cookie')
}
const SID = 'sid-earlier'
const validSession = { ...session(SID), agent_id: 'agent-1', title: 'Cleared session' }
function history(earlier: boolean) {
  return page(earlier ? [entry(1, { agent_id: 'agent-1', type: 'system', role: 'system', content: 'Conversation context cleared' })] : [], {
    sessionId: SID, earlier,
    info: earlier ? { stopped_at_clear: true, stop_reason: 'clear', latest_clear: { entry_id: 'history-1', history_position: pos(1) } } : {},
  })
}

describe('canonical has_earlier page carriers — D3/D10/D15', () => {
  let fetchSpy: ReturnType<typeof vi.fn>
  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookieLocal('__Host-csrf=test-csrf-token')
  })
  afterEach(() => {
    vi.unstubAllGlobals(); sessionStorage.clear(); restoreCookieLocal(); vi.resetModules()
  })

  it('GET /sessions/{id} — available detail carries true with exact first-position continuation', async () => {
    const fixture = availableDetail(history(true), validSession)
    expectValidDetail(fixture, [pos(1)], 1)
    fetchSpy.mockResolvedValueOnce(new Response(JSON.stringify(fixture), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    const { fetchSessionDetail } = await import('./sessions')
    const detail: unknown = await fetchSessionDetail(SID)
    expect(detail).toMatchObject({ history_state: { status: 'available' }, has_earlier: true, has_later: false, next_before: pos(1), page_info: fixture.page_info })
  })

  it('GET /sessions/{id} — available terminal history explicitly records false without a phantom cursor', async () => {
    const fixture = availableDetail(history(false), validSession)
    expectValidDetail(fixture, [], 0)
    fetchSpy.mockResolvedValueOnce(new Response(JSON.stringify(fixture), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    const { fetchSessionDetail } = await import('./sessions')
    const detail: unknown = await fetchSessionDetail(SID)
    expect(detail).toMatchObject({ history_state: { status: 'available' }, messages: [], has_earlier: false, has_later: false, page_info: fixture.page_info })
    expect(Object.hasOwn(detail as object, 'next_before')).toBe(false)
    expect(Object.hasOwn(detail as object, 'next_after')).toBe(false)
  })

  it('GET /sessions/{id}/messages — true page metadata wins over a false retired header', async () => {
    const fixture = history(true)
    expectValidPage(fixture, [pos(1)], 1)
    fetchSpy.mockResolvedValueOnce(new Response(JSON.stringify(fixture), { status: 200, headers: { 'Content-Type': 'application/json', 'X-Has-Earlier-Messages': 'false' } }))
    const { fetchSessionMessages } = await import('./sessions')
    expect(await fetchSessionMessages(SID)).toMatchObject({ has_earlier: true, next_before: pos(1), page_info: fixture.page_info })
  })

  it('GET /sessions/{id}/messages — false page metadata wins over a true retired header', async () => {
    const fixture = history(false)
    expectValidPage(fixture, [], 0)
    fetchSpy.mockResolvedValueOnce(new Response(JSON.stringify(fixture), { status: 200, headers: { 'Content-Type': 'application/json', 'X-Has-Earlier-Messages': 'true' } }))
    const { fetchSessionMessages } = await import('./sessions')
    expect(await fetchSessionMessages(SID)).toMatchObject({ entries: [], has_earlier: false, has_later: false, page_info: fixture.page_info })
  })

  it('GET /sessions/{id}/messages — a missing retired header does not invent earlier continuation', async () => {
    const fixture = history(false)
    expectValidPage(fixture, [], 0)
    fetchSpy.mockResolvedValueOnce(new Response(JSON.stringify(fixture), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    const { fetchSessionMessages } = await import('./sessions')
    const result: unknown = await fetchSessionMessages(SID)
    expect(result).toMatchObject({ entries: [], has_earlier: false, has_later: false, page_info: fixture.page_info })
    expect(Object.hasOwn(result as object, 'next_before')).toBe(false)
  })
})
