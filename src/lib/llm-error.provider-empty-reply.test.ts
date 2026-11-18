/**
 * llm-error.provider-empty-reply.test.ts — issue #1664 B10 (founder
 * decisions 2026-11-07, Q2): a used-up empty-stream turn ends in the new
 * provider_empty_reply code with its OWN sentence, attributed to `provider`
 * and retryable; the fixed "The model returned an empty response…" text is
 * not used for this case. Oracle: /workspace/logs/fix-1664/verify-report.md
 * §B10 — never the catalogue.
 *
 * Round 2 (team-lead dispatch): the asserted sentence and attribution are
 * READ from the generated catalogue (llm-error-messages.ts), never retyped —
 * the copy-guard (pkg/api/generated/llm_error_no_hardcopy_test.go::
 * TestLLMErrorMessages_NoHandCopiesOutsideTheCatalogue) fails any hand-copy
 * of a catalogue sentence elsewhere. Assertion strength unchanged: exact
 * equality, and `codeToMessage` must RECOGNIZE the code (a miss would
 * return the unknown-code fallback instead).
 */
import { describe, it, expect } from 'vitest'
import {
  llmErrorUserAttributions,
  llmErrorUserMessages,
} from './api/generated/llm-error-messages'
import { codeToAttribution, codeToMessage, type LLMErrorCode } from './llm-error'

const providerEmptyReply: LLMErrorCode = 'provider_empty_reply'

// Retired legacy text, not a catalogue sentence — embedded only for this
// absence assertion (the copy-guard explicitly allows absent-assertions).
const OLD_FIXED_TEXT =
  'The model returned an empty response. This may indicate a provider error or token limit.'

describe('provider_empty_reply — the empty-stream verdict (issue #1664 B10, founder Q2)', () => {
  it('maps the code to the catalogue sentence (recognized, not the unknown fallback)', () => {
    expect(codeToMessage(providerEmptyReply)).toBe(llmErrorUserMessages[providerEmptyReply])
  })

  it('is attributed to the provider', () => {
    expect(llmErrorUserAttributions[providerEmptyReply]).toBe('provider')
    expect(codeToAttribution[providerEmptyReply]).toBe(llmErrorUserAttributions[providerEmptyReply])
  })

  it('does not reuse the fixed empty-response sentence', () => {
    expect(codeToMessage(providerEmptyReply)).not.toBe(OLD_FIXED_TEXT)
  })
})
