// RED pack — WC-RESUME RED·U13/U14, unit U14/UF3 (DEL-F42).
//
// Spec source: docs/internal/specs/session-core-spec.md
//   DEL-F42 — old branch: `src/components/chat/GoalPillTray.tsx::GoalPill`,
//             `GoalPillTray`, `describePillState` — "Live guards skip the
//             retired goal state `queued`; type narrowing also excludes it."
//             Replacement: "Remove retired queued-goal type/guards, not
//             execution-slot helper waiting. Current live goal branches
//             remain; backend owns enum."
//   BDD-12.1 — "K check detects forbidden alias and known symbol."
//
// Oracle provenance: the spec's replacement column. The retired `queued`
// GOAL state's guards are removed from GoalPillTray once the backend's goal
// enum (C-GOAL, backend-owned) no longer emits it. The K (source) instrument
// the spec names for DEL rows is a source scan — the three code-level
// `queued` GUARD forms must be gone. Matching the guard expressions (not the
// bare word `queued`, which also appears in comments) keeps the check precise.
//
// ORDERING NOTE for GREEN: the `Exclude<GoalStatusFrame['state'], 'queued'>`
// narrowing is a NO-OP once the generated enum drops `queued`, and the
// `=== 'queued'` / `!== 'queued'` comparisons become TS errors against a
// narrower union — so this removal is gated on the backend C-GOAL enum
// change landing (U14 backend half, not yet landed). This test is RED until
// then; it is not a frontend-only change.

import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const SOURCE = readFileSync(resolve(here, 'GoalPillTray.tsx'), 'utf8')

describe('U14/UF3 DEL-F42 — the retired queued-goal guards are gone from GoalPillTray', () => {
  it('no longer type-narrows the goal state union to exclude `queued`', () => {
    expect(SOURCE).not.toMatch(/Exclude<\s*GoalStatusFrame\['state'\]\s*,\s*'queued'\s*>/)
  })

  it('no longer guards against a `queued` goal state in GoalPill', () => {
    expect(SOURCE).not.toMatch(/frame\.state\s*===\s*'queued'/)
  })

  it('no longer filters `queued` frames out of the tray', () => {
    expect(SOURCE).not.toMatch(/frame\.state\s*!==\s*'queued'/)
  })

  it('still renders the live goal branches (replacement present, not merely deleted)', () => {
    // Current live goal branches remain per DEL-F42 — the pill-state mapping
    // is untouched by the removal.
    expect(SOURCE).toContain('describePillState')
    expect(SOURCE).toContain('GOAL_TERMINAL_STATES')
  })
})
