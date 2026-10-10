// RED pack — WC-FIX RED-F, unit UF4 (DEL-F03, DEL-F04).
//
// Spec source: docs/internal/specs/session-core-spec.md — FR-038 / BDD-12.1
// ("K check detects forbidden alias and known symbol"; every DELETE inventory
// ID removed with canonical replacement/nothing), and the DEL-F rows in the
// Delete Requirements table:
//
//   DEL-F03  src/lib/api/agents.ts::isWorker — third test `a.type === 'worker'`,
//            "for stale payloads" — REMOVE. Replacement: `Subagent` /
//            `subagent_3p` tests already present.
//   DEL-F04  src/lib/agentKind.ts::isWorkerType, agentKindFlags — legacy
//            `worker` recognition and `(type === 'worker' && executor.kind ===
//            'external-cli')` classification — REMOVE. Replacement: modern
//            type-based native/external classification.
//
// Oracle provenance: the SPEC's replacement column, i.e. the legacy `worker`
// type string must no longer be recognised as a worker — only the two modern
// wire enum values `Subagent` and `subagent_3p`. The expected values below are
// derived from that column, NOT read off the current implementation (which is
// exactly the code this pack is RED against).
//
// The legacy `worker` defensive fallback (oracle note): FR-038's greenfield
// ruling removes compatibility-only branches outright; the `worker` type is a
// build-time/seed config constant the gateway does not emit, so "canonical
// replacement / nothing" for these IDs is an EMPTY legacy case. There is no
// "guessed" post-removal behaviour to assert beyond "not a worker".

import { describe, it, expect } from 'vitest'
import { isWorkerType, agentKindFlags } from './agentKind'
import { isWorker } from './api/agents'

describe('UF4/DEL-F03 — isWorker no longer recognises the legacy `worker` type', () => {
  it('returns false for the legacy `worker` type string (DEL-F03 removal)', () => {
    // Spec (DEL-F03): the third `a.type === 'worker'` test is deleted; only
    // Subagent / subagent_3p remain.
    expect(isWorker({ type: 'worker' })).toBe(false)
  })

  it('still returns true for the two modern wire enum worker types (canonical positive control)', () => {
    expect(isWorker({ type: 'Subagent' })).toBe(true)
    expect(isWorker({ type: 'subagent_3p' })).toBe(true)
  })

  it('returns false for a non-worker / missing type (canonical negative control)', () => {
    expect(isWorker({ type: 'Main' })).toBe(false)
    expect(isWorker({})).toBe(false)
  })
})

describe('UF4/DEL-F04 — isWorkerType no longer recognises the legacy `worker` type', () => {
  it('returns false for the legacy `worker` type string (DEL-F04 removal)', () => {
    // Spec (DEL-F04): legacy `worker` recognition is removed.
    expect(isWorkerType('worker')).toBe(false)
  })

  it('still returns true for Subagent and subagent_3p (canonical positive control)', () => {
    expect(isWorkerType('Subagent')).toBe(true)
    expect(isWorkerType('subagent_3p')).toBe(true)
  })

  it('returns false for Main/core/system and missing/null (canonical negative controls)', () => {
    expect(isWorkerType('Main')).toBe(false)
    expect(isWorkerType('core')).toBe(false)
    expect(isWorkerType('system')).toBe(false)
    expect(isWorkerType(undefined)).toBe(false)
    expect(isWorkerType(null)).toBe(false)
  })
})

describe('UF4/DEL-F04 — agentKindFlags drops the legacy `worker` classification', () => {
  it('classifies a legacy `worker` agent as NOT a worker, native, or external', () => {
    // Spec (DEL-F04): `(type === 'worker' && executor.kind === 'external-cli')`
    // classification is removed; `worker` is no longer a recognised kind.
    expect(agentKindFlags({ type: 'worker', locked: false })).toEqual({
      isLocked: false,
      isWorker: false,
      isExternal: false,
      isNativeWorker: false,
      isSystem: false,
    })
  })

  it('classifies a legacy `worker` with an external-cli executor as NOT external (the removed executor branch)', () => {
    // The specific removed branch: a legacy worker whose executor.kind is
    // 'external-cli' used to resolve isExternal:true. Spec replacement is
    // modern type-based classification only — the type alone can no longer
    // make it external.
    expect(
      agentKindFlags({ type: 'worker', locked: false, executor: { kind: 'external-cli' } }),
    ).toEqual({
      isLocked: false,
      isWorker: false,
      isExternal: false,
      isNativeWorker: false,
      isSystem: false,
    })
  })

  it('still classifies a native Subagent as a non-external worker (canonical positive control)', () => {
    expect(agentKindFlags({ type: 'Subagent', locked: false })).toEqual({
      isLocked: false,
      isWorker: true,
      isExternal: false,
      isNativeWorker: true,
      isSystem: false,
    })
  })

  it('still classifies a subagent_3p agent as an external worker (canonical positive control)', () => {
    expect(agentKindFlags({ type: 'subagent_3p', locked: false })).toEqual({
      isLocked: false,
      isWorker: true,
      isExternal: true,
      isNativeWorker: false,
      isSystem: false,
    })
  })
})
