import { describe, expect, it } from 'vitest'
import { CancelFrame } from '@/lib/api/generated/ws-schemas'

// §3 /workspace/logs/1534/arch-decision.md. Real generated schema, with
// positive controls so rejecting all shown objects is never a false green.
const legacy = { type: 'cancel', session_id: 'schema-1534' }
const checkpoint = { message_id: 'm', turn_id: 't', code_points: 0 }

describe('#1534 generated cancel checkpoint contract', () => {
  it('keeps old cancel valid without shown', () => {
    expect(CancelFrame.safeParse(legacy)).toMatchObject({ success: true, data: legacy })
  })

  it('accepts and retains the entire valid shown record', () => {
    const input = { ...legacy, shown: checkpoint }
    expect(CancelFrame.safeParse(input)).toMatchObject({ success: true, data: input })
  })

  it('accepts an empty placeholder checkpoint with no invented IDs', () => {
    const input = { ...legacy, shown: { code_points: 0 } }
    expect(CancelFrame.safeParse(input)).toMatchObject({ success: true, data: input })
  })

  it.each(['message_id', 'turn_id'] as const)('accepts %s of exactly 1024 characters', field => {
    const input = { ...legacy, shown: { ...checkpoint, [field]: 'x'.repeat(1024) } }
    expect(CancelFrame.safeParse(input)).toMatchObject({ success: true, data: input })
  })

  it.each(['message_id', 'turn_id'] as const)('rejects %s of 1025 characters for the nested ID bound', field => {
    const control = { ...legacy, shown: { ...checkpoint, [field]: 'x'.repeat(1024) } }
    expect(CancelFrame.safeParse(control), 'instrument: legal shown must pass').toMatchObject({ success: true, data: control })
    const invalid = CancelFrame.safeParse({ ...legacy, shown: { ...checkpoint, [field]: 'x'.repeat(1025) } })
    expect(invalid.success).toBe(false)
    if (!invalid.success) expect(invalid.error.issues).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: 'too_big', path: ['shown', field], maximum: 1024 }),
    ]))
  })

  it.each([-1, 0.5, '1'])('rejects malformed code_points %s at the nested counter field', codePoints => {
    const control = { ...legacy, shown: checkpoint }
    expect(CancelFrame.safeParse(control), 'instrument: legal counter must pass').toMatchObject({ success: true, data: control })
    const invalid = CancelFrame.safeParse({ ...legacy, shown: { code_points: codePoints } })
    expect(invalid.success).toBe(false)
    if (!invalid.success) expect(invalid.error.issues.map(issue => issue.path)).toContainEqual(['shown', 'code_points'])
  })
})
