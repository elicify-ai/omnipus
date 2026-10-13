// K pack — U12 / DEL-F20 (the frontend consumer of the retired explicit
// session-close WS frame).
//
// Spec source: docs/internal/specs/session-core-spec.md — DEL-F20 ("Retained
// explicit-close type surface and live `session_close_ack` classification. No
// frontend sender found; explicit close retired.") and DEL-08 (the backend
// deletes the explicit session-close frame, handler and ack).
//
// Oracle provenance: the spec's own boundary — "remove generated union/ack
// consumers via backend-owned specs". Type re-exports are erased at runtime, so
// the K (compile/source) instrument the spec names is a source scan: `ws.ts`
// must no longer import or re-export `SessionCloseFrame` / `SessionCloseAckFrame`,
// while the canonical generated frame surface it still needs stays present. The
// `SESSION_SCOPED_FRAME_TYPES` classification is a live runtime value, so it is
// asserted directly rather than scanned.
//
// Note: the backend contract/generated removal (DEL-08) is a separate lane; this
// pack asserts only the frontend consumer surface, which is what DEL-F20 owns.

import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'
import { SESSION_SCOPED_FRAME_TYPES } from '@/store/chat/runtime-state'

const here = dirname(fileURLToPath(import.meta.url))
const WS_SOURCE = readFileSync(resolve(here, 'ws.ts'), 'utf8')

describe('DEL-F20 — the retired explicit session-close type surface is gone from ws.ts', () => {
  it('no longer imports or re-exports SessionCloseFrame / SessionCloseAckFrame', () => {
    expect(WS_SOURCE).not.toMatch(/\bSessionCloseFrame\b/)
    expect(WS_SOURCE).not.toMatch(/\bSessionCloseAckFrame\b/)
  })

  it('still consumes the canonical generated frame types it needs (replacement present, not merely deleted)', () => {
    for (const canonical of ['DevicePairingResponseFrame', 'CancelStageFrame', 'ServerFrame']) {
      expect(WS_SOURCE, `canonical generated type ${canonical} must still be used in ws.ts`).toContain(canonical)
    }
  })
})

describe('DEL-F20 — session_close_ack is no longer a session-scoped frame', () => {
  it('drops session_close_ack from SESSION_SCOPED_FRAME_TYPES', () => {
    expect(SESSION_SCOPED_FRAME_TYPES.has('session_close_ack')).toBe(false)
  })

  it('keeps the neighbouring session-scoped classifications (not a wholesale delete)', () => {
    expect(SESSION_SCOPED_FRAME_TYPES.has('cancel_stage')).toBe(true)
    expect(SESSION_SCOPED_FRAME_TYPES.has('system_overload')).toBe(true)
  })
})
