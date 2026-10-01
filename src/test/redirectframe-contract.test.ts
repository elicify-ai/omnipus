// redirectframe-contract.test.ts — RED wave (qa-lead,
// test/a-redirect-transport-red): Zod pins for the landed generated
// RedirectFrame schema (src/lib/api/generated/ws-schemas.ts).
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation):
//   - contracts/components/schemas/RedirectFrame.yaml: {type: const
//     "redirect", session_id: 1..128 required, instruction: minLength 1 /
//     maxLength 16384 / pattern \S}, additionalProperties: false, NO scope.
//   - The Go-side mirror pins live in
//     pkg/api/generated/redirectframe_contract_test.go; the NBSP and astral
//     cases here are the OTHER HALF of that divergence evidence: JS `\s`
//     includes U+00A0 (Go RE2 `\S` does not) and Zod `.max` counts UTF-16
//     code units (JSON Schema maxLength counts code points; the server's
//     runtime ceiling counts UTF-8 bytes). The pairs together are the
//     evidence requested by the dispatch brief; the contract PROSE is not
//     edited by QA.
//
// These are GREEN pins (the schema already landed); they guard the contract
// against regeneration drift, and the divergence pairs are labeled
// `characterization test` where they pin cross-validator semantics.

import { describe, it, expect } from 'vitest'
import { RedirectFrame as redirectFrameSchema } from '@/lib/api/generated/ws-schemas'
import type { RedirectFrame as RedirectFrameWire } from '@/lib/api/generated/asyncapi-types'

const VALID = {
  type: 'redirect' as const,
  session_id: 'helper-session-9',
  instruction: 'focus on the failing tests',
}

describe('generated RedirectFrame Zod schema (ws-schemas)', () => {
  it('parses a valid frame and round-trips it exactly', () => {
    const parsed = redirectFrameSchema.safeParse(VALID)
    expect(parsed.success).toBe(true)
    if (parsed.success) {
      expect(parsed.data).toEqual(VALID)
      // The parsed data satisfies the generated wire interface (Constraint #8:
      // generated types only).
      const wire: RedirectFrameWire = parsed.data
      expect(wire.type).toBe('redirect')
    }
  })

  it('rejects an extra "scope" property (additionalProperties: false — D9 row 2 fixes scope)', () => {
    const withScope = { ...VALID, scope: 'helper' }
    const parsed = redirectFrameSchema.safeParse(withScope)
    expect(parsed.success).toBe(false)
    if (!parsed.success) {
      expect(parsed.error.issues.length).toBeGreaterThan(0)
    }
  })

  it('rejects wrong type literals (const "redirect")', () => {
    expect(redirectFrameSchema.safeParse({ ...VALID, type: 'cancel' }).success).toBe(false)
    expect(redirectFrameSchema.safeParse({ ...VALID, type: 'redirect_x' }).success).toBe(false)
  })

  it('enforces session_id 1..128 (boundary: empty / 128 / 129)', () => {
    expect(redirectFrameSchema.safeParse({ ...VALID, session_id: '' }).success).toBe(false)
    expect(redirectFrameSchema.safeParse({ ...VALID, session_id: 's'.repeat(128) }).success).toBe(true)
    expect(redirectFrameSchema.safeParse({ ...VALID, session_id: 's'.repeat(129) }).success).toBe(false)
  })

  it('enforces instruction maxLength 16384 counted as UTF-16 code units (boundary: 16384 / 16385 / empty)', () => {
    expect(redirectFrameSchema.safeParse({ ...VALID, instruction: 'a'.repeat(16384) }).success).toBe(true)
    expect(redirectFrameSchema.safeParse({ ...VALID, instruction: 'a'.repeat(16385) }).success).toBe(false)
    expect(redirectFrameSchema.safeParse({ ...VALID, instruction: '' }).success).toBe(false)
  })

  it('rejects ASCII-whitespace-only instructions (pattern \\S)', () => {
    expect(redirectFrameSchema.safeParse({ ...VALID, instruction: '\t \n  ' }).success).toBe(false)
  })

  // characterization test: JavaScript's `\s` INCLUDES U+00A0, so the Zod layer
  // rejects a NBSP-only instruction — while the Go JSON-Schema layer (RE2
  // `\S`, ASCII-only) ACCEPTS the same string (pinned in
  // pkg/api/generated/redirectframe_contract_test.go). The pair is the
  // cross-validator divergence evidence; the authoritative Unicode-aware
  // nonblank check is the server runtime's obligation.
  it('rejects NBSP-only instructions (JS \\s includes U+00A0 — diverges from the Go RE2 layer)', () => {
    expect(redirectFrameSchema.safeParse({ ...VALID, instruction: '\u00A0'.repeat(4) }).success).toBe(false)
    expect(redirectFrameSchema.safeParse({ ...VALID, instruction: '\u2003' }).success).toBe(false)
  })

  // characterization test: Zod `.max(16384)` counts UTF-16 code units, so 8192
  // astral emoji (16384 units, 16384 UTF-8 bytes, 8192 code points) pass all
  // three layers, while 8193 emoji (16386 units) FAIL here although the JSON
  // Schema layer accepts them (8193 code points ≤ 16384 — pinned Go-side) and
  // the server's 16384-UTF-8-byte runtime ceiling refuses them (16386 bytes).
  // The three layers disagree exactly at this input — that is the evidence.
  it('counts astral characters as 2 UTF-16 units each (8192 emoji pass, 8193 fail — diverges from the code-point layer)', () => {
    expect(redirectFrameSchema.safeParse({ ...VALID, instruction: '\u{1F600}'.repeat(8192) }).success).toBe(true)
    expect(redirectFrameSchema.safeParse({ ...VALID, instruction: '\u{1F600}'.repeat(8193) }).success).toBe(false)
  })

  it('round-trips multibyte instructions exactly', () => {
    const instruction = '先 export the CSV — 报告'
    const parsed = redirectFrameSchema.safeParse({ ...VALID, instruction })
    expect(parsed.success).toBe(true)
    if (parsed.success) {
      expect(parsed.data.instruction).toBe(instruction)
    }
  })
})
