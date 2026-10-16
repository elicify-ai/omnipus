import { describe, expect, it } from 'vitest'
import * as featureSwitches from './featureSwitches'
import {
  AGENT_PRIVATE_SECRETS_ENABLED,
  INLINE_TOOL_APPROVALS_ENABLED,
  ROOMS_ENABLED,
} from './featureSwitches'

// Founder decision 2026-10-14 (stage 1), team-lead follow-up (g): keep
// unserved features hidden and use the release approval pop-up. Import the
// REAL shipped module: no mock, importActual substitute or copied constants.
// Mutations are assigned to a fresh CHECK instance, never applied by RED.
describe('founder decision 2026-10-14 — stage-1 shipped switches', () => {
  // CHECK mutant: featureSwitches.ts::ROOMS_ENABLED false → true.
  it('[S1-g rooms] ships ROOMS_ENABLED as exactly false', () => {
    expect(ROOMS_ENABLED).toBe(false)
  })

  // WU-4 server is landed; WU-5's private-secrets list is still unavailable.
  // CHECK mutant: featureSwitches.ts::SECRET_CARDS_ENABLED true → false.
  it('[WU-4 secrets] ships SECRET_CARDS_ENABLED as exactly true', () => {
    expect(featureSwitches.SECRET_CARDS_ENABLED).toBe(true)
  })

  // CHECK mutant: featureSwitches.ts::AGENT_PRIVATE_SECRETS_ENABLED false → true.
  it('[S1-g secrets] ships AGENT_PRIVATE_SECRETS_ENABLED as exactly false', () => {
    expect(AGENT_PRIVATE_SECRETS_ENABLED).toBe(false)
  })

  // CHECK mutant: featureSwitches.ts::INLINE_TOOL_APPROVALS_ENABLED false → true.
  it('[S1-g approvals] ships INLINE_TOOL_APPROVALS_ENABLED as exactly false', () => {
    expect(INLINE_TOOL_APPROVALS_ENABLED).toBe(false)
  })
})
