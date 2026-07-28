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
  fromModelPrefix,
  getLLMErrorDisplay,
  readEntryIdFromFrame,
  readLLMErrorFromFrame,
  readLLMErrorFromReplayFrame,
  sanitizeLegacyErrorMessage,
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

  // Attribution: errors whose cause is upstream of Omnipus carry the
  // "From the model:" prefix so the user knows the failure is not a product
  // bug. Failures we own outright (5xx, fallback) carry no prefix;
  // `network` is ambiguous and carries no prefix because the wording
  // ("check the network") already disambiguates. Mirrors Go userMessages
  // in pkg/agent/translate_error.go so the bubble and the persisted
  // transcript stay in one voice. See
  // docs/internal/uat/ADR-051-rev4-error-copy-review.md for rationale.
  it('upstream-caused codes start with the "From the model:" attribution prefix', () => {
    const upstreamCodes: LLMErrorCode[] = [
      'media_unsupported',
      'provider_rejected',
      'rate_limited',
      'content_policy',
      'context_too_long',
      'tool_args',
      'schema',
      'unknown',
    ]
    for (const code of upstreamCodes) {
      expect(
        codeToDisplay[code].startsWith('From the model:'),
        `code "${code}" copy must start with "From the model:" prefix; got: ${codeToDisplay[code]}`,
      ).toBe(true)
    }
  })

  it('ambiguous codes (network) do NOT carry the prefix', () => {
    expect(codeToDisplay.network.startsWith('From the model:')).toBe(false)
    // Wording already disambiguates; "check the network" cues the user.
    expect(codeToDisplay.network.toLowerCase()).toContain('network')
  })

  it('exposes the fromModelPrefix constant so renderers can style it', () => {
    // Renderers may want to split the bubble copy on this prefix and render
    // the attribution in a muted `<small>` tag. Pin the exact wording so
    // the prefix match between the constant and every prefixed copy stays
    // a compile-time invariant.
    expect(fromModelPrefix).toBe('From the model:')
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

// D5 (UAT Site 3) — chat.ts's case 'error' legacy fallback must not surface
// raw Go-internal/protocol strings verbatim.
describe('llm-error — sanitizeLegacyErrorMessage (D5)', () => {
  it('collapses a Go `<component>: <verb>` style protocol string to a generic message', () => {
    expect(sanitizeLegacyErrorMessage('browser_control: attach before requesting control')).toBe(
      'Something went wrong — please try again.',
    )
    expect(sanitizeLegacyErrorMessage('browser_attach: agent_id and session_id are required')).toBe(
      'Something went wrong — please try again.',
    )
    expect(sanitizeLegacyErrorMessage('workspace_setup: kickoff failed')).toBe(
      'Something went wrong — please try again.',
    )
  })

  it('collapses a raw {"type":...} JSON-ish wire frame string', () => {
    expect(sanitizeLegacyErrorMessage('{"type":"error","message":"boom"}')).toBe(
      'Something went wrong — please try again.',
    )
  })

  it('passes deliberately-authored, human-readable backend strings through unchanged', () => {
    // Real Message: literals from pkg/gateway/websocket.go — none have a
    // bare identifier immediately followed by a colon.
    expect(sanitizeLegacyErrorMessage('workspace setup has already run')).toBe('workspace setup has already run')
    expect(sanitizeLegacyErrorMessage('session not found')).toBe('session not found')
    expect(sanitizeLegacyErrorMessage('cancel failed: session already closed')).toBe(
      'cancel failed: session already closed',
    )
    expect(sanitizeLegacyErrorMessage('malformed workspace_setup_kickoff metadata')).toBe(
      'malformed workspace_setup_kickoff metadata',
    )
    expect(sanitizeLegacyErrorMessage('this agent is a worker and cannot be a chat target — workers are invoked via delegation')).toBe(
      'this agent is a worker and cannot be a chat target — workers are invoked via delegation',
    )
  })

  it('passes an empty string through unchanged (never crashes on empty input)', () => {
    expect(sanitizeLegacyErrorMessage('')).toBe('')
  })
})
