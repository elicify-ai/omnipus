import { describe, expect, it } from 'vitest'
import {
  AGENT_PRIVATE_SECRETS_ENABLED,
  INLINE_TOOL_APPROVALS_ENABLED,
  ROOMS_ENABLED,
} from './featureSwitches'

// Founder decision 2026-10-14 (stage 1), team-lead follow-up (g): keep
// unserved features hidden and use the release approval pop-up. Import the
// REAL shipped module: no mock, importActual substitute or copied constants.
// Mutations are assigned to a fresh CHECK instance, never applied by RED.
//
// SUPERSESSION (founder decision 2026-10-18, Train 3 re-cut, recorded in
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/PLAN-omnipus-L1-v2-20261013.md
// line 143: "WU-15 rooms + room screens, rooms switched on"): the rooms pin is
// re-aimed from exactly-false to exactly-true — same strength, opposite pole.
// The secrets and approvals switches keep the 2026-10-14 stage-1 pin.
describe('founder decision 2026-10-14 — stage-1 shipped switches', () => {
  // CHECK mutant: featureSwitches.ts::ROOMS_ENABLED true → false.
  it('[S1-g rooms] ships ROOMS_ENABLED as exactly true', () => {
    expect(ROOMS_ENABLED).toBe(true)
  })

  // CHECK mutant: featureSwitches.ts::AGENT_PRIVATE_SECRETS_ENABLED false → true.
  it('[S1-g secrets] ships AGENT_PRIVATE_SECRETS_ENABLED as exactly false', () => {
    expect(AGENT_PRIVATE_SECRETS_ENABLED).toBe(false)
  })

  // SUPERSESSION (Train 3, amendment section 1 / bundle B3): inline approvals
  // ship ON — the card shows inline in its root chat instead of the app-wide
  // ToolApprovalModal. CHECK mutant: true → false.
  it('[S1-g approvals] ships INLINE_TOOL_APPROVALS_ENABLED as exactly true', () => {
    expect(INLINE_TOOL_APPROVALS_ENABLED).toBe(true)
  })
})
