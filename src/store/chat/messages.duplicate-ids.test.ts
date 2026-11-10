// S3-09: getMessages collapses a repeated message id (assistant-ui throws on
// one), but the collapse must leave a trace - otherwise a real ordering or
// persistence bug that repeats ids is invisible.
import { beforeEach, describe, expect, it, vi } from 'vitest'

const logDiagnostic = vi.hoisted(() => vi.fn())
vi.mock('@/lib/telemetry', async (orig) => ({ ...(await orig<typeof import('@/lib/telemetry')>()), logDiagnostic }))

import { getMessages, resetDuplicateIdReports } from './messages'
import type { ChatMessage } from './types'

const msg = (id: string, content: string): ChatMessage =>
  ({ id, role: 'assistant', content, timestamp: '2026-10-12T00:00:00Z', status: 'done' }) as ChatMessage

describe('getMessages duplicate ids', () => {
  beforeEach(() => {
    logDiagnostic.mockClear()
    resetDuplicateIdReports()
  })

  it('keeps the last occurrence at its position (behaviour unchanged)', () => {
    const out = getMessages({
      messageOrder: ['a', 'b', 'a'],
      messagesById: { a: msg('a', 'late'), b: msg('b', 'mid') },
    })
    expect(out.map((m) => m.id)).toEqual(['b', 'a'])
    expect(out[1].content).toBe('late')
  })

  it('reports the collapse with the repeated id, once per id however often the projection runs', () => {
    const bucket = {
      messageOrder: ['a', 'b', 'a', 'b', 'b'],
      messagesById: { a: msg('a', 'x'), b: msg('b', 'y') },
    }
    getMessages(bucket)
    getMessages(bucket)
    getMessages(bucket)
    expect(logDiagnostic).toHaveBeenCalledTimes(2)
    const ids = logDiagnostic.mock.calls.map(([event, fields]) => [event, fields.messageId, fields.occurrences])
    expect(ids).toEqual([
      ['chatDuplicateMessageIdCollapsed', 'b', 3],
      ['chatDuplicateMessageIdCollapsed', 'a', 2],
    ])
  })

  it('negative control: an order with unique ids reports nothing', () => {
    getMessages({ messageOrder: ['a', 'b'], messagesById: { a: msg('a', 'x'), b: msg('b', 'y') } })
    expect(logDiagnostic).not.toHaveBeenCalled()
  })
})
