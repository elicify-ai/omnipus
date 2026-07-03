// agentKind.test.ts — coverage for the single source of truth for
// "what kind of agent is this?" UI gating (docs/internal/architecture/
// agent-types-field-matrix.md). Exercises agentKindFlags across every
// kind the wire enum + legacy config constants can produce, plus the two
// standalone predicates it's built from.

import { describe, it, expect } from 'vitest'
import { agentKindFlags, isExternalType, isWorkerType } from './agentKind'

describe('isWorkerType', () => {
  it('is true for Subagent, subagent_3p, and the legacy worker constant', () => {
    expect(isWorkerType('Subagent')).toBe(true)
    expect(isWorkerType('subagent_3p')).toBe(true)
    expect(isWorkerType('worker')).toBe(true)
  })

  it('is false for Main, core, other type strings, and missing/null', () => {
    expect(isWorkerType('Main')).toBe(false)
    expect(isWorkerType('core')).toBe(false)
    expect(isWorkerType('system')).toBe(false)
    expect(isWorkerType(undefined)).toBe(false)
    expect(isWorkerType(null)).toBe(false)
  })
})

describe('isExternalType', () => {
  it('is true only for subagent_3p', () => {
    expect(isExternalType('subagent_3p')).toBe(true)
  })

  it('is false for a legacy worker even when the caller means an external-cli executor — it is TYPE-STRING-ONLY and cannot see executor.kind', () => {
    expect(isExternalType('worker')).toBe(false)
  })

  it('is false for Main, Subagent, core, and missing/null', () => {
    expect(isExternalType('Main')).toBe(false)
    expect(isExternalType('Subagent')).toBe(false)
    expect(isExternalType('core')).toBe(false)
    expect(isExternalType(undefined)).toBe(false)
    expect(isExternalType(null)).toBe(false)
  })
})

describe('agentKindFlags', () => {
  it('classifies a locked core agent', () => {
    expect(agentKindFlags({ type: 'core', locked: true })).toEqual({
      isLocked: true,
      isWorker: false,
      isExternal: false,
      isNativeWorker: false,
    })
  })

  it('classifies an unlocked core agent', () => {
    expect(agentKindFlags({ type: 'core', locked: false })).toEqual({
      isLocked: false,
      isWorker: false,
      isExternal: false,
      isNativeWorker: false,
    })
  })

  it('classifies a Main agent', () => {
    expect(agentKindFlags({ type: 'Main', locked: false })).toEqual({
      isLocked: false,
      isWorker: false,
      isExternal: false,
      isNativeWorker: false,
    })
  })

  it('classifies a native Subagent as a worker that is NOT external', () => {
    expect(agentKindFlags({ type: 'Subagent', locked: false })).toEqual({
      isLocked: false,
      isWorker: true,
      isExternal: false,
      isNativeWorker: true,
    })
  })

  it('classifies a subagent_3p agent as external regardless of its executor value (TYPE-based, not executor-based)', () => {
    expect(
      agentKindFlags({ type: 'subagent_3p', locked: false, executor: { kind: 'external-cli' } }),
    ).toEqual({
      isLocked: false,
      isWorker: true,
      isExternal: true,
      isNativeWorker: false,
    })
    // Even a nonsensical native/absent executor on a subagent_3p agent
    // still resolves external — the type alone decides for this kind.
    expect(
      agentKindFlags({ type: 'subagent_3p', locked: false, executor: { kind: 'native' } }),
    ).toEqual({
      isLocked: false,
      isWorker: true,
      isExternal: true,
      isNativeWorker: false,
    })
    expect(agentKindFlags({ type: 'subagent_3p', locked: false })).toEqual({
      isLocked: false,
      isWorker: true,
      isExternal: true,
      isNativeWorker: false,
    })
  })

  it('classifies a legacy worker with a native (or absent) executor as a native worker', () => {
    expect(agentKindFlags({ type: 'worker', locked: false })).toEqual({
      isLocked: false,
      isWorker: true,
      isExternal: false,
      isNativeWorker: true,
    })
    expect(
      agentKindFlags({ type: 'worker', locked: false, executor: { kind: 'native' } }),
    ).toEqual({
      isLocked: false,
      isWorker: true,
      isExternal: false,
      isNativeWorker: true,
    })
  })

  it('classifies a legacy worker with an external-cli executor as external — the one case isExternalType alone would get wrong', () => {
    expect(
      agentKindFlags({ type: 'worker', locked: false, executor: { kind: 'external-cli' } }),
    ).toEqual({
      isLocked: false,
      isWorker: true,
      isExternal: true,
      isNativeWorker: false,
    })
  })

  it('defaults every flag to false for a missing or null type', () => {
    expect(agentKindFlags({ type: undefined })).toEqual({
      isLocked: false,
      isWorker: false,
      isExternal: false,
      isNativeWorker: false,
    })
    expect(agentKindFlags({ type: null })).toEqual({
      isLocked: false,
      isWorker: false,
      isExternal: false,
      isNativeWorker: false,
    })
  })

  it('handles a bare empty object (no fields at all)', () => {
    expect(agentKindFlags({})).toEqual({
      isLocked: false,
      isWorker: false,
      isExternal: false,
      isNativeWorker: false,
    })
  })

  it('treats locked=null the same as locked=false — only locked===true counts', () => {
    expect(agentKindFlags({ type: 'core', locked: null }).isLocked).toBe(false)
  })
})
