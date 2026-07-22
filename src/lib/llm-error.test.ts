/**
 * llm-error.test.ts — ADR-051 unit tests for the SPA-side LLM error
 * translation module.
 *
 * Covers: code→display map completeness, getLLMErrorDisplay verbose gating,
 * unknown-code fallback, and the safe optional frame readers.
 */

import { describe, it, expect } from 'vitest'
import {
  codeToDisplay,
  codeToMessage,
  getLLMErrorDisplay,
  readEntryIdFromFrame,
  readLLMErrorFromFrame,
  readLLMErrorFromReplayFrame,
  type LLMErrorCode,
} from './llm-error'

const ALL_CODES: LLMErrorCode[] = [
  'media_unsupported',
  'provider_rejected',
  'rate_limited',
  'network',
  'content_policy',
  'context_too_long',
  'unknown',
]

describe('llm-error — codeToDisplay map', () => {
  it('has an entry for every backend code (no missing translations)', () => {
    for (const code of ALL_CODES) {
      expect(codeToDisplay[code], `code "${code}" must have display copy`).toBeTypeOf('string')
      expect(codeToDisplay[code].length, `code "${code}" copy must be non-empty`).toBeGreaterThan(0)
    }
  })

  it('codeToMessage returns the matching copy for a known code', () => {
    expect(codeToMessage('rate_limited')).toBe(codeToDisplay.rate_limited)
    expect(codeToMessage('content_policy')).toBe(codeToDisplay.content_policy)
  })

  it('codeToMessage falls back to the "unknown" copy for an unrecognized code', () => {
    // Forward-compat: a newer backend emits a code this SPA build doesn't know.
    expect(codeToMessage('something_new_in_v2')).toBe(codeToDisplay.unknown)
  })

  it('codeToMessage falls back to "unknown" for undefined/null', () => {
    expect(codeToMessage(undefined)).toBe(codeToDisplay.unknown)
    // Null is passed defensively — some downstream callers treat absent as null.
    expect(codeToMessage(null as unknown as undefined)).toBe(codeToDisplay.unknown)
  })
})

describe('llm-error — getLLMErrorDisplay verbose gating', () => {
  it('always returns the code→display message regardless of verbose setting', () => {
    const le = { code: 'network' as const, message: 'raw wire msg', retryable: true, detail: 'detail text' }
    expect(getLLMErrorDisplay(le, false).message).toBe(codeToDisplay.network)
    expect(getLLMErrorDisplay(le, true).message).toBe(codeToDisplay.network)
  })

  it('omits detail when verbose is false, even if detail is non-empty', () => {
    const le = { code: 'provider_rejected', message: 'm', retryable: false, detail: 'sensitive internals' }
    const out = getLLMErrorDisplay(le, false)
    expect(out.detail).toBeUndefined()
  })

  it('omits detail when verbose is true but detail is empty/whitespace', () => {
    const empty = { code: 'rate_limited', message: 'm', retryable: true, detail: '' }
    const ws = { code: 'rate_limited', message: 'm', retryable: true, detail: '   ' }
    expect(getLLMErrorDisplay(empty, true).detail).toBeUndefined()
    // Whitespace-only detail is treated as absent (mirrors the renderer's trim guard).
    expect(getLLMErrorDisplay(ws, true).detail).toBeUndefined()
  })

  it('includes detail only when verbose is true AND detail is non-empty', () => {
    const le = { code: 'context_too_long', message: 'm', retryable: false, detail: 'token count: 999999' }
    const out = getLLMErrorDisplay(le, true)
    expect(out.detail).toBe('token count: 999999')
  })

  it('omits detail when the input has no detail field at all (replay payload shape)', () => {
    // LLMErrorReplay has no detail. Passing that shape must not crash and
    // must yield detail===undefined regardless of verbose.
    const replay = { code: 'unknown', message: 'm', retryable: false }
    expect(getLLMErrorDisplay(replay, true).detail).toBeUndefined()
    expect(getLLMErrorDisplay(replay, true).message).toBe(codeToDisplay.unknown)
  })

  it('handles an unrecognized code in getLLMErrorDisplay (forward-compat)', () => {
    const out = getLLMErrorDisplay({ code: 'future_code', message: 'm', retryable: false }, true)
    expect(out.message).toBe(codeToDisplay.unknown)
  })
})

describe('llm-error — readLLMErrorFromFrame (live ErrorFrame)', () => {
  it('reads a well-formed typed payload off a frame', () => {
    const frame = {
      type: 'error',
      message: 'legacy',
      payload: { llm_error: { code: 'network', message: 'conn reset', retryable: true, detail: 'tcp RST' } },
    }
    const out = readLLMErrorFromFrame(frame)
    expect(out).toEqual({ code: 'network', message: 'conn reset', retryable: true, detail: 'tcp RST' })
  })

  it('returns undefined when the frame has no payload', () => {
    // Legacy frames (pre-ADR-051) — the entire typed payload is absent.
    expect(readLLMErrorFromFrame({ type: 'error', message: 'legacy' })).toBeUndefined()
  })

  it('returns undefined when payload exists but llm_error is absent', () => {
    expect(readLLMErrorFromFrame({ type: 'error', payload: {} })).toBeUndefined()
  })

  it('returns undefined when llm_error exists but is malformed (missing required fields)', () => {
    expect(readLLMErrorFromFrame({ payload: { llm_error: { code: 'network' } } })).toBeUndefined()
    expect(readLLMErrorFromFrame({ payload: { llm_error: null } })).toBeUndefined()
    expect(readLLMErrorFromFrame({ payload: { llm_error: 'string-not-object' } })).toBeUndefined()
  })

  it('omits detail from the returned shape when the wire payload omits it', () => {
    const out = readLLMErrorFromFrame({
      payload: { llm_error: { code: 'unknown', message: 'm', retryable: false } },
    })
    expect(out).toBeDefined()
    expect(out!.detail).toBeUndefined()
  })

  it('returns undefined for a null / non-object frame', () => {
    expect(readLLMErrorFromFrame(null)).toBeUndefined()
    expect(readLLMErrorFromFrame(undefined)).toBeUndefined()
    expect(readLLMErrorFromFrame('string')).toBeUndefined()
  })
})

describe('llm-error — readLLMErrorFromReplayFrame', () => {
  it('reads a well-formed replay payload (no detail field)', () => {
    const out = readLLMErrorFromReplayFrame({
      type: 'replay_error',
      entry_id: 'e1',
      payload: { llm_error: { code: 'provider_rejected', message: 'm', retryable: false } },
    })
    expect(out).toEqual({ code: 'provider_rejected', message: 'm', retryable: false })
    // LLMErrorReplay has no detail field on its type — the returned shape
    // mirrors that (no detail key at all).
    expect(out).not.toHaveProperty('detail')
  })

  it('returns undefined for legacy replay frames without a typed payload', () => {
    expect(readLLMErrorFromReplayFrame({ type: 'replay_error', message: 'legacy' })).toBeUndefined()
  })
})

describe('llm-error — readEntryIdFromFrame', () => {
  it('reads entry_id when present and a string', () => {
    expect(readEntryIdFromFrame({ entry_id: 'abc-123' })).toBe('abc-123')
  })

  it('returns undefined when entry_id is absent or non-string', () => {
    expect(readEntryIdFromFrame({})).toBeUndefined()
    expect(readEntryIdFromFrame({ entry_id: 42 })).toBeUndefined()
    expect(readEntryIdFromFrame({ entry_id: null })).toBeUndefined()
    expect(readEntryIdFromFrame(null)).toBeUndefined()
    expect(readEntryIdFromFrame(undefined)).toBeUndefined()
  })
})
