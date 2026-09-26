// providerStatus.test.ts — the six-value usability rule (ADR-068 FR-019 /
// FR-034/FR-038; the enum itself lives in
// contracts/components/schemas/Provider.yaml).
//
// isProviderUsable is the ONE home of "can this provider serve a turn" —
// every picker and filter site must call it instead of copying a status
// comparison. The signed_in-invisible hotfix happened because four pickers
// kept their own `status === 'connected'` copy and only some of them ever
// learned about the seventh... no, the sixth state: `signed_in`. This file
// pins the predicate against ALL SIX wire values, so a seventh status added
// to Provider.yaml fails loudly here (and at typecheck, via
// PROVIDER_STATUS_LABELS' exhaustive Record) instead of silently sliding
// through a stale copy somewhere.

import { describe, it, expect } from 'vitest'
import {
  PROVIDER_STATUS_LABELS,
  USABLE_PROVIDER_STATUSES,
  isProviderUsable,
  type ProviderStatus,
} from './providerStatus'

// The six wire values, straight off Provider.yaml — the closed set this
// module must account for completely.
const ALL_SIX: ProviderStatus[] = [
  'connected',
  'disconnected',
  'error',
  'unknown-provider',
  'signed_in',
  'expired',
]

describe('providerStatus — isProviderUsable, all six wire values', () => {
  it('USABLE: connected (API key configured and resolvable)', () => {
    expect(isProviderUsable('connected')).toBe(true)
  })

  it('USABLE: signed_in (ADR-068 FR-034 — a live subscription session serves turns exactly like a connected key)', () => {
    expect(isProviderUsable('signed_in')).toBe(true)
  })

  it('NOT usable: disconnected (no key configured)', () => {
    expect(isProviderUsable('disconnected')).toBe(false)
  })

  it('NOT usable: error (configured but upstream non-retryable failure)', () => {
    expect(isProviderUsable('error')).toBe(false)
  })

  it('NOT usable: unknown-provider (ADR-067 FR-016 — models are [] by construction)', () => {
    expect(isProviderUsable('unknown-provider')).toBe(false)
  })

  it('NOT usable: expired (a LAPSED sign-in session — the user must sign in again; offering its models would offer models that cannot run)', () => {
    expect(isProviderUsable('expired')).toBe(false)
  })

  it('USABLE_PROVIDER_STATUSES is exactly {connected, signed_in} — the rule in one array', () => {
    expect(USABLE_PROVIDER_STATUSES).toEqual(['connected', 'signed_in'])
  })

  it('PROVIDER_STATUS_LABELS keys cover exactly the six wire values — a seventh contract status fails here and at typecheck (exhaustive Record), never silently', () => {
    expect(Object.keys(PROVIDER_STATUS_LABELS).sort()).toEqual([...ALL_SIX].sort())
  })
})
