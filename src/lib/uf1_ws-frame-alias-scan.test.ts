// RED pack — WC-FIX RED-F, unit UF1 (DEL-F14–F18, DEL-F19).
//
// Spec source: docs/internal/specs/session-core-spec.md — BDD-12.1 ("K check
// detects forbidden alias and known symbol") and the DEL-F14–F18 / DEL-F19 rows.
//
//   DEL-F14–F18  Legacy `Ws*Frame` type aliases plus their imports/casts:
//                `WsSubagentStartFrame`, `WsSubagentEndFrame`,
//                `WsReplayMessageFrame`, `WsRateLimitFrame`,
//                `WsToolApprovalRequiredFrame`, `WsSessionStateFrame`,
//                `WsReceiveFrame`. Replacement: use the generated
//                SubagentStartFrame / SubagentEndFrame / ReplayMessageFrame /
//                RateLimitFrame / ToolApprovalRequiredFrame / SessionStateFrame /
//                ServerFrame directly.
//   DEL-F19      `WsConnectionCallbacks.onFrame` legacy single-frame callback and
//                the `_flushBatch` fallback that loops it. Replacement:
//                `onFrames(batch)` is already defined and preferred.
//
// Oracle provenance: the spec's replacement column. Type aliases are erased at
// runtime, so the K (compile/source) instrument the spec itself names is a
// source scan: the forbidden alias *declarations* and the legacy `onFrame`
// callback must be gone, while the canonical generated type names remain the
// only frame-type surface. (This mirrors the repo's existing source-scan K
// tests, e.g. src/test/canonicalToolNames.test.ts.)
//
// Note: DEL-F20 (`SessionCloseFrame`/`SessionCloseAckFrame`) follows U12 and is
// deliberately NOT asserted absent here.

import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const WS_SOURCE = readFileSync(resolve(here, 'ws.ts'), 'utf8')

describe('UF1/DEL-F14–F18 — legacy Ws*Frame aliases are gone from ws.ts', () => {
  it('declares no `export type Ws…Frame = …` legacy alias', () => {
    // Spec: the seven legacy aliases are removed. Matching the DECLARATION
    // (not incidental comment text) keeps the check precise.
    const legacyAliasDeclarations = WS_SOURCE.match(/export type Ws\w+Frame\s*=/g) ?? []
    expect(legacyAliasDeclarations).toEqual([])
  })

  it('still consumes the canonical generated frame types (replacement present, not merely deleted)', () => {
    // The replacement surface per the spec's DEL-F14–F18 row.
    for (const canonical of [
      'SubagentStartFrame',
      'SubagentEndFrame',
      'ReplayMessageFrame',
      'RateLimitFrame',
      'ToolApprovalRequiredFrame',
      'SessionStateFrame',
      'ServerFrame',
    ]) {
      expect(WS_SOURCE, `canonical generated type ${canonical} must be used in ws.ts`).toContain(canonical)
    }
  })
})

describe('UF1/DEL-F19 — the legacy single-frame onFrame callback is gone', () => {
  it('does not declare a legacy `onFrame` single-frame callback', () => {
    // Word-boundary match so `onFrames` (the kept batch callback) and
    // `requestAnimationFrame` do not false-positive.
    expect(WS_SOURCE).not.toMatch(/\bonFrame\b/)
  })

  it('still declares the preferred onFrames batch callback (replacement present)', () => {
    expect(WS_SOURCE).toContain('onFrames')
  })
})
