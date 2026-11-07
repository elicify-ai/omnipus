/**
 * llm-error.provider-empty-reply.test.ts — issue #1664 B10 (founder
 * decisions 2026-11-07, Q2): a used-up empty-stream turn ends in the new
 * provider_empty_reply code with its OWN sentence —
 *   "The model provider ended its answer without sending anything. Retry —
 *    if it keeps happening, open Verbose chat for details."
 * attributed to `provider` and retryable; the fixed "The model returned an
 * empty response…" text is not used for this case. Oracle:
 * /workspace/logs/fix-1664/verify-report.md §B10 — never the catalogue.
 *
 * The code joins the contract enum with the B10 wave; until then the lookup
 * is driven through the wire-typed alias (double cast), so this file
 * typechecks before and after the regeneration. RED today: the catalogue
 * has no entry, so the unknown-code fallback renders instead. When the enum
 * lands, src/lib/llm-error.test.ts's ALL_CODES list gains the code too —
 * that list's bidirectional equality with the generated enum is the
 * designed guard forcing the conscious addition.
 */
import { describe, it, expect } from 'vitest'
import { codeToAttribution, codeToMessage, type LLMErrorCode } from './llm-error'

const providerEmptyReply = 'provider_empty_reply' as string as LLMErrorCode

const B10_SENTENCE =
  'The model provider ended its answer without sending anything. Retry — if it keeps happening, open Verbose chat for details.'
const OLD_FIXED_TEXT =
  'The model returned an empty response. This may indicate a provider error or token limit.'

describe('provider_empty_reply — the empty-stream verdict (issue #1664 B10, founder Q2)', () => {
  it('maps the code to its own sentence', () => {
    expect(codeToMessage(providerEmptyReply)).toBe(B10_SENTENCE)
  })

  it('is attributed to the provider', () => {
    expect(codeToAttribution[providerEmptyReply]).toBe('provider')
  })

  it('does not reuse the fixed empty-response sentence', () => {
    expect(codeToMessage(providerEmptyReply)).not.toBe(OLD_FIXED_TEXT)
  })
})
