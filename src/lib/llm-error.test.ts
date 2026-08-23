/**
 * llm-error.test.ts — ADR-051 unit tests for the SPA-side LLM error
 * translation module.
 *
 * Covers: code→display map completeness, getLLMErrorDisplay verbose gating,
 * unknown-code fallback, and the safe optional frame readers.
 */

import { describe, it, expect } from 'vitest'
import { llmErrorAttributionValues, llmErrorCodes } from './api/generated/llm-error-messages'
import {
  codeToAttribution,
  codeToDisplay,
  codeToMessage,
  getLLMErrorDisplay,
  readEntryIdFromFrame,
  readLLMErrorFromFrame,
  readLLMErrorFromReplayFrame,
  sanitizeLegacyErrorMessage,
  type LLMErrorCode,
} from './llm-error'

// Every code on the wire, listed explicitly so adding one is a conscious
// test-side decision rather than something the suite absorbs silently.
//
// This literal used to name 7 of the then-9 codes and nothing noticed — the
// per-code loops below simply skipped `tool_args` and `schema` forever. The
// equality assertion immediately after is the fix: the list can go stale, but
// it can no longer go stale QUIETLY.
const ALL_CODES: LLMErrorCode[] = [
  'media_unsupported',
  'provider_rejected',
  'request_too_large',
  'provider_auth_failed',
  'rate_limited',
  'network',
  'content_policy',
  'context_too_long',
  'tool_args',
  'schema',
  'agent_not_configured',
  'workspace_unavailable',
  'model_unavailable',
  // ADR-066 / ADR-067 / ADR-068 (A-CONTRACT commit):
  'needs_provider',
  'model_unassigned',
  'turn_canceled',
  'turn_timed_out',
  'context_unrecoverable',
  'context_window_unknown',
  'unknown',
]

describe('llm-error — codeToDisplay map', () => {
  it('ALL_CODES covers exactly the generated contract enum (no silent under-testing)', () => {
    expect([...ALL_CODES].sort()).toEqual([...llmErrorCodes].sort())
  })

  it('has an entry for every backend code (no missing translations)', () => {
    for (const code of ALL_CODES) {
      expect(codeToDisplay[code], `code "${code}" must have display copy`).toBeTypeOf('string')
      expect(codeToDisplay[code].trim().length, `code "${code}" copy must be non-empty`).toBeGreaterThan(0)
    }
  })

  it('has a valid attribution for every backend code', () => {
    for (const code of ALL_CODES) {
      expect(
        llmErrorAttributionValues as readonly string[],
        `code "${code}" must carry an attribution from the contract vocabulary`,
      ).toContain(codeToAttribution[code])
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

  // The blanket "From the model:" prefix is DELETED. It blamed the model for
  // every failure — including the ones Omnipus causes (an oversized request we
  // built) and the ones an operator fixes in Settings (a bad API key).
  // Attribution now lives in each sentence, plus a machine-readable tag.
  it('no copy carries the retired "From the model:" blanket prefix', () => {
    for (const code of ALL_CODES) {
      expect(
        codeToDisplay[code].includes('From the model:'),
        `code "${code}" must not reintroduce the retired blanket prefix; got: ${codeToDisplay[code]}`,
      ).toBe(false)
    }
  })

  // ── The class-killer ───────────────────────────────────────────────────────
  //
  // When the fault is OURS (`product`) or the operator's SETTINGS (`config`),
  // the copy must not push the user at remedies that cannot possibly work.
  // Telling someone to switch models because we built a malformed request, or
  // to rephrase their perfectly fine sentence because an API key is wrong,
  // sends them off to burn time on the wrong thing — and hides the real defect
  // behind user error. Each ban below is a sentence that was in the shipped
  // copy before this change.
  const OURS: LLMErrorCode[] = ALL_CODES.filter(
    (code) => codeToAttribution[code] === 'product' || codeToAttribution[code] === 'config',
  )

  it('has at least one product-attributed and one config-attributed code (guard is not vacuous)', () => {
    expect(ALL_CODES.some((c) => codeToAttribution[c] === 'product')).toBe(true)
    expect(ALL_CODES.some((c) => codeToAttribution[c] === 'config')).toBe(true)
  })

  it('never tells the user to switch models when the fault is ours or the operator’s', () => {
    // Unqualified model-shopping only. "switch to a model with a larger limit"
    // is fine: it names the property that would actually help.
    const banned = ['switch models', 'switch model.', 'different model', 'another model', 'pick a model']
    for (const code of OURS) {
      const copy = codeToDisplay[code].toLowerCase()
      for (const phrase of banned) {
        expect(
          copy.includes(phrase),
          `code "${code}" is attributed "${codeToAttribution[code]}" but tells the user to shop for a model ("${phrase}"); got: ${codeToDisplay[code]}`,
        ).toBe(false)
      }
    }
  })

  it('never asks the user to rephrase when the fault is ours or the operator’s', () => {
    for (const code of OURS) {
      const copy = codeToDisplay[code].toLowerCase()
      for (const phrase of ['rephras', 'reword', 'try asking differently']) {
        expect(
          copy.includes(phrase),
          `code "${code}" is attributed "${codeToAttribution[code]}" but blames the user's wording ("${phrase}"); got: ${codeToDisplay[code]}`,
        ).toBe(false)
      }
    }
  })

  it('never tells the user to retry a config fault (retrying cannot fix a setting)', () => {
    // `product` may legitimately suggest a retry — a request we built badly can
    // come out right the second time. A `config` fault cannot: the same wrong
    // API key, the same missing workspace membership, the same failure.
    for (const code of ALL_CODES.filter((c) => codeToAttribution[c] === 'config')) {
      const copy = codeToDisplay[code].toLowerCase()
      for (const phrase of ['retry', 'try again']) {
        expect(
          copy.includes(phrase),
          `code "${code}" is a config fault but tells the user to ${phrase}; got: ${codeToDisplay[code]}`,
        ).toBe(false)
      }
    }
  })

  it('never tells the user to contact support (Omnipus is self-hosted; there is no support desk)', () => {
    const banned = [
      'contact support',
      'contact us',
      'customer support',
      'support team',
      'our support',
      'reach out',
      'get in touch',
      'file a ticket',
      'open a ticket',
      'help desk',
      'helpdesk',
    ]
    for (const code of ALL_CODES) {
      const copy = codeToDisplay[code].toLowerCase()
      for (const phrase of banned) {
        expect(
          copy.includes(phrase),
          `code "${code}" points the user at a support desk that does not exist ("${phrase}"); got: ${codeToDisplay[code]}`,
        ).toBe(false)
      }
    }
  })

  it('attributes the three split codes to their real fault owners', () => {
    // The single `provider_rejected` bucket used to swallow all three.
    expect(codeToAttribution.request_too_large).toBe('product')
    expect(codeToAttribution.provider_auth_failed).toBe('config')
    expect(codeToAttribution.agent_not_configured).toBe('config')
    // …and each says where to go.
    expect(codeToDisplay.provider_auth_failed.toLowerCase()).toContain('settings')
    expect(codeToDisplay.agent_not_configured.toLowerCase()).toContain('workspace')
  })

  it('ambiguous codes (network) name where to look instead of assigning blame', () => {
    expect(codeToAttribution.network).toBe('ambiguous')
    expect(codeToDisplay.network.toLowerCase()).toContain('internet connection')
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
