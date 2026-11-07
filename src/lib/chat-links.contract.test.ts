/**
 * chat-links contract CHECK (c), plus the 2026-10-29 architect clarifications.
 *
 * Oracle: docs/internal/specs/chat-links-spec.md Contract section items (a)–(c)
 * and the clarification that LibraryRef.path and OpenInLibraryFrame.path have
 * minLength 1, and session_id is 1..255 on OpenInLibraryFrame and ChatPresenceFrame.
 *
 * The runtime parser is src/lib/ws.ts::parseFrameSafe (WsFrame from
 * src/lib/api/generated/ws-schemas.ts). Grep of the zod fragment is not this check.
 */
import { describe, expect, it, beforeEach } from 'vitest'
import {
  getDroppedFrameCount,
  getUnknownFrameTypeCount,
  parseFrameSafe,
  resetDroppedFrameCount,
  resetUnknownFrameTypeCount,
} from './ws'
import { SESSION_SCOPED_FRAME_TYPES } from '@/store/chat/runtime-state'

const REQ = '0123456789abcdef0123456789abcdef'

function sid(n: number): string {
  return 's'.repeat(n)
}

function mediaFrame(libraryRef?: { workspace_id: string; path: string }) {
  const part: Record<string, unknown> = {
    type: 'file',
    url: '/api/v1/media/plan',
    filename: 'plan.md',
    content_type: 'text/markdown',
  }
  if (libraryRef) part.library_ref = libraryRef
  return {
    type: 'media',
    session_id: 'sess-1',
    parts: [part],
  }
}

function openFrame(overrides: Record<string, unknown> = {}) {
  return {
    type: 'open_in_library',
    session_id: 'sess-1',
    call_id: 'call-1',
    request_id: REQ,
    workspace_id: 'W',
    path: 'notes/plan.md',
    ...overrides,
  }
}

function presenceFrame(overrides: Record<string, unknown> = {}) {
  return {
    type: 'chat_presence',
    session_id: 'sess-1',
    watching: true,
    ...overrides,
  }
}

beforeEach(() => {
  resetDroppedFrameCount()
  resetUnknownFrameTypeCount()
})

function expectAccepted(frame: unknown, label: string) {
  const parsed = parseFrameSafe(frame)
  expect(parsed, `${label} must parse`).not.toBeNull()
  expect(getDroppedFrameCount(), `${label} dropped count`).toBe(0)
  expect(getUnknownFrameTypeCount(), `${label} unknown count`).toBe(0)
  return parsed
}

function expectRejected(frame: unknown, label: string) {
  const parsed = parseFrameSafe(frame)
  expect(parsed, `${label} must be rejected`).toBeNull()
  const dropped = getDroppedFrameCount()
  const unknown = getUnknownFrameTypeCount()
  expect(dropped + unknown, `${label} must increment a drop counter`).toBeGreaterThan(0)
}

describe('chat-links contract parser (CHECK c)', () => {
  it('parses a media frame with library_ref and one without it', () => {
    const withRef = expectAccepted(
      mediaFrame({ workspace_id: 'W', path: 'notes/plan.md' }),
      'media with library_ref',
    ) as { parts?: Array<{ library_ref?: { workspace_id: string; path: string } }> }
    expect(withRef.parts?.[0]?.library_ref).toEqual({
      workspace_id: 'W',
      path: 'notes/plan.md',
    })

    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
    const without = expectAccepted(mediaFrame(), 'media without library_ref') as {
      parts?: Array<{ library_ref?: unknown }>
    }
    expect(without.parts?.[0]?.library_ref).toBeUndefined()
  })

  it('parses open_in_library with a 32-hex request_id and session_id length 1 and 255', () => {
    const parsed = expectAccepted(openFrame(), 'open_in_library') as {
      type?: string
      request_id?: string
      path?: string
    }
    expect(parsed.type).toBe('open_in_library')
    expect(parsed.request_id).toBe(REQ)
    expect(parsed.path).toBe('notes/plan.md')

    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
    expectAccepted(openFrame({ session_id: 's' }), 'session_id length 1')

    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
    expectAccepted(openFrame({ session_id: sid(255) }), 'session_id length 255')
  })

  it('rejects open_in_library whose request_id is missing or not 32 lowercase hex', () => {
    const { request_id: _drop, ...missing } = openFrame()
    expectRejected(missing, 'missing request_id')

    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
    expectRejected(openFrame({ request_id: '0123456789ABCDEF0123456789abcdef' }), 'uppercase request_id')

    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
    expectRejected(openFrame({ request_id: REQ.slice(0, 31) }), '31-hex request_id')

    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
    expectRejected(openFrame({ request_id: REQ + 'a' }), '33-hex request_id')
  })

  it('rejects an empty Library path on open_in_library and on library_ref', () => {
    // Architect clarification 2026-10-29: path minLength 1. Empty is never valid.
    expectRejected(openFrame({ path: '' }), 'open_in_library path empty')

    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
    expectRejected(
      mediaFrame({ workspace_id: 'W', path: '' }),
      'media library_ref.path empty',
    )
  })

  it('rejects session_id outside 1..255 on open_in_library and chat_presence', () => {
    expectRejected(openFrame({ session_id: '' }), 'open session_id empty')
    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
    expectRejected(openFrame({ session_id: sid(256) }), 'open session_id 256')

    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
    expectRejected(presenceFrame({ session_id: '' }), 'presence session_id empty')
    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
    expectRejected(presenceFrame({ session_id: sid(256) }), 'presence session_id 256')
    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
    expectAccepted(presenceFrame({ session_id: sid(255) }), 'presence session_id 255')
  })

  it('rejects chat_presence that omits watching', () => {
    const { watching: _drop, ...missing } = presenceFrame()
    expectRejected(missing, 'chat_presence missing watching')
  })

  it('registers open_in_library as session-scoped and does not register chat_presence', () => {
    // Spec: add open_in_library only. chat_presence is client→server and is NOT
    // added to SESSION_SCOPED_FRAME_TYPES.
    expect(SESSION_SCOPED_FRAME_TYPES.has('open_in_library')).toBe(true)
    expect(SESSION_SCOPED_FRAME_TYPES.has('chat_presence')).toBe(false)
  })
})
