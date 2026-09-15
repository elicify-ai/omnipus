import { describe, it, expect } from 'vitest'
import {
  normalizeTruncationReason,
  getMessageStatusSuffix,
  INTERRUPTED_SUFFIX_TEXT,
  CUT_OFF_SUFFIX_TEXT,
} from './truncation'

describe('normalizeTruncationReason (ADR-087 D2 legacy-default rule)', () => {
  it('returns undefined when not truncated', () => {
    expect(normalizeTruncationReason(undefined, undefined)).toBeUndefined()
    expect(normalizeTruncationReason(false, undefined)).toBeUndefined()
    // Defensive: a reason with truncated:false/undefined is still not
    // truncated — the reason field means nothing without the flag.
    expect(normalizeTruncationReason(false, 'max_output_tokens')).toBeUndefined()
  })

  it('legacy default: truncated:true with no reason means "cancelled"', () => {
    expect(normalizeTruncationReason(true, undefined)).toBe('cancelled')
  })

  it('passes through an explicit reason on a truncated entry', () => {
    expect(normalizeTruncationReason(true, 'max_output_tokens')).toBe('max_output_tokens')
    expect(normalizeTruncationReason(true, 'cancelled')).toBe('cancelled')
  })
})

describe('getMessageStatusSuffix (ADR-087 D1)', () => {
  it('returns null for a non-assistant message regardless of fields', () => {
    expect(getMessageStatusSuffix({ role: 'user', status: 'interrupted' })).toBeNull()
    expect(getMessageStatusSuffix({ role: 'system', truncationReason: 'max_output_tokens' })).toBeNull()
    expect(getMessageStatusSuffix({})).toBeNull()
  })

  it('returns null for a plain done assistant message with no truncation', () => {
    expect(getMessageStatusSuffix({ role: 'assistant', status: 'done' })).toBeNull()
  })

  it('returns the interrupted suffix for status:"interrupted" (FR-21, pre-existing)', () => {
    expect(getMessageStatusSuffix({ role: 'assistant', status: 'interrupted' })).toBe(
      INTERRUPTED_SUFFIX_TEXT,
    )
  })

  it('returns the interrupted suffix for truncationReason:"cancelled" even when status is not "interrupted"', () => {
    expect(
      getMessageStatusSuffix({ role: 'assistant', status: 'done', truncationReason: 'cancelled' }),
    ).toBe(INTERRUPTED_SUFFIX_TEXT)
  })

  it('returns the cut-off suffix for truncationReason:"max_output_tokens"', () => {
    expect(
      getMessageStatusSuffix({ role: 'assistant', status: 'done', truncationReason: 'max_output_tokens' }),
    ).toBe(CUT_OFF_SUFFIX_TEXT)
    expect(CUT_OFF_SUFFIX_TEXT).toBe('(cut off at the output limit)')
  })

  it('D1 precedence: status "interrupted" wins over truncationReason "max_output_tokens" — exactly one suffix, never both', () => {
    const suffix = getMessageStatusSuffix({
      role: 'assistant',
      status: 'interrupted',
      truncationReason: 'max_output_tokens',
    })
    expect(suffix).toBe(INTERRUPTED_SUFFIX_TEXT)
    expect(suffix).not.toBe(CUT_OFF_SUFFIX_TEXT)
  })
})
