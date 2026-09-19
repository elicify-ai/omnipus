// generateId — CSPRNG contract tests. The id serves as an unguessable
// storage/URL key for uploads, so generateId must draw entropy only from Web
// Crypto (128-bit when via getRandomValues) and must THROW rather than mint a
// predictable id when no secure source exists. Each test stubs
// globalThis.crypto per branch and pins the entropy source by construction:
// a deterministic getRandomValues stub must produce a deterministic suffix,
// which a Math.random()-style implementation cannot.

import { describe, it, expect } from 'vitest'
import { generateId } from './constants'

type CryptoStub = Record<string, unknown>

// withCrypto installs a stub as the global crypto object for the duration of
// fn, then restores the previous property descriptor — deleting the injected
// property when none existed before, so the stub never leaks into other tests.
// (globalThis.crypto is accessor-only in some engines, so plain assignment
// can throw in strict mode; defineProperty/deleteProperty always work.)
function withCrypto<T>(stub: CryptoStub | undefined, fn: () => T): T {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'crypto')
  Object.defineProperty(globalThis, 'crypto', {
    value: stub,
    configurable: true,
    writable: true,
  })
  try {
    return fn()
  } finally {
    if (original) {
      Object.defineProperty(globalThis, 'crypto', original)
    } else {
      Reflect.deleteProperty(globalThis, 'crypto')
    }
  }
}

// Deterministic CSPRNG stand-in: fills with 0xAB so the generated suffix is
// exactly the hex of the stubbed bytes — proves the suffix came from the stub.
const deterministicGetRandomValues = {
  getRandomValues: (array: Uint8Array): Uint8Array => {
    array.fill(0xab)
    return array
  },
}

describe('generateId CSPRNG contract', () => {
  it('prefers crypto.randomUUID when available', () => {
    const id = withCrypto({ randomUUID: () => 'fixed-uuid-value' }, () => generateId())
    expect(id).toBe('fixed-uuid-value')
  })

  it('falls back to 128-bit crypto.getRandomValues when randomUUID is absent, with distinct ids', () => {
    let nextByte = 0
    const varying = {
      getRandomValues: (array: Uint8Array): Uint8Array => {
        array.fill(0)
        array[0] = nextByte++
        return array
      },
    }
    const [pinned, second] = [
      withCrypto(deterministicGetRandomValues, () => generateId()),
      withCrypto(varying, () => generateId()),
    ]
    // 16 CSPRNG bytes = 32 hex chars after the dash-free timestamp part.
    const [tsPart, entropyPart] = pinned.split('-')
    expect(tsPart).toMatch(/^[0-9a-z]+$/)
    expect(entropyPart).toBe('ab'.repeat(16))
    expect(second).not.toBe(pinned)
  })

  it('refuses to mint an id when no secure random source exists', () => {
    // value: undefined makes `typeof crypto === 'undefined'` true in the module.
    expect(() => withCrypto(undefined, () => generateId())).toThrow(/secure random source/)
  })

  it('restores globalThis.crypto exactly, even when no crypto property existed before', () => {
    const original = Object.getOwnPropertyDescriptor(globalThis, 'crypto')
    try {
      Reflect.deleteProperty(globalThis, 'crypto')
      const id = withCrypto(deterministicGetRandomValues, () => generateId())
      expect(typeof id).toBe('string')
      // No prior descriptor existed → withCrypto must have removed its stub.
      expect(Object.getOwnPropertyDescriptor(globalThis, 'crypto')).toBeUndefined()
    } finally {
      if (original) Object.defineProperty(globalThis, 'crypto', original)
    }
  })

  it('unstubbed environment yields distinct ids (real Web Crypto)', () => {
    expect(generateId()).not.toBe(generateId())
  })
})
