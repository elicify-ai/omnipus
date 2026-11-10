// RED pack — WC-RESUME RED·U13/U14, unit U14 (FR-039; DEL-F39–40 — KEEP half).
//
// Spec source: docs/internal/specs/session-core-spec.md
//   FR-039 — "KEEP isGoalRunning and goalStatus state/writers/read used by
//            Stop in ChatScreen, including waiting_on_user Stop-pause; do not
//            remove a control dependency as an indicator cleanup."
//   DEL-F39–40 — "KEEP src/lib/goalActivity.ts::isGoalRunning and ChatScreen's
//            goalStatus read Stop uses, with necessary shared writers/state."
//   C-GOAL UI — "Keep isGoalRunning and the scalar writer/state/read Stop
//            needs."
//   BDD-12.5   — "Existing isGoalRunning(goalStatus) keeps Stop reachable
//            during running goal idle gap and false for waiting_on_user."
//
// ORACLE PROVENANCE: the spec's KEEP column — the pre-existing Stop-control
// contract, unchanged by U14/UF3.
//
// GREEN-ON-CURRENT NOTE (honesty, per the RED brief): every assertion in this
// file PASSES on the current tree. It is a KEEP / regression GUARD, not a RED
// assertion — its job is to fail loudly if UF3's "delete the latest-goal-wins
// indicator" sweep over-deletes the Stop control dependency the spec
// explicitly preserves (removing `isGoalRunning` or the scalar `goalStatus`
// read as collateral). It belongs to U14's KEEP half; it is NOT evidence of
// new behaviour.

import { describe, it, expect } from 'vitest'
import { isGoalRunning } from './goalActivity'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

function frame(state: GoalStatusFrame['state']): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: 'sid',
    goal_id: 'g1',
    condition: 'c',
    round: 0,
    max_rounds: 20,
    latest_reason: '',
    active_loops: 1,
    cap: 16,
    state,
  }
}

describe('U14 KEEP — isGoalRunning keeps Stop reachable during a running goal idle gap', () => {
  it.each(['active', 're-planning', 'judging', 'judge_unavailable'] as const)(
    'returns true for a goal the keeper will resume on its own (%s)',
    (state) => {
      // BDD-12.5: "isGoalRunning(goalStatus) keeps Stop reachable during
      // running goal idle gap". In the idle gap between turns isStreaming is
      // false, yet the keeper resumes the goal — Stop must still reach it.
      expect(isGoalRunning(frame(state))).toBe(true)
    },
  )

  it.each(['waiting_on_user', 'done', 'failed', 'cleared'] as const)(
    'returns false for a goal that is parked/terminal (%s)',
    (state) => {
      // BDD-12.5: "false for waiting_on_user" — a Stop-paused goal parks until
      // the user speaks; it is not independently running.
      expect(isGoalRunning(frame(state))).toBe(false)
    },
  )

  it('returns false for null/undefined goalStatus (no active goal)', () => {
    expect(isGoalRunning(null)).toBe(false)
    expect(isGoalRunning(undefined)).toBe(false)
  })
})
