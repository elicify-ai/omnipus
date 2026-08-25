// providerStatus.ts — human copy for the six wire provider states
// (ADR-068 FR-038; the enum itself lives in contracts/components/schemas/Provider.yaml).
//
// One module so every surface that shows a provider's state — the Settings
// row badge, the Remove dialog's new-default candidates, the sign-in panels
// (T068-26) — reads the SAME word for the same wire value. The enum is never
// shown raw.

import type { components } from '@/lib/api/generated/openapi-types'

/** The six provider statuses, straight off the wire contract. */
export type ProviderStatus = components['schemas']['Provider']['status']

/** Wire value → the word the operator reads. Never render the enum itself. */
export const PROVIDER_STATUS_LABELS: Record<ProviderStatus, string> = {
  connected: 'Connected',
  disconnected: 'Not configured',
  error: 'Error',
  'unknown-provider': 'Unknown provider',
  signed_in: 'Signed in',
  expired: 'Session expired',
}

export function providerStatusLabel(status: ProviderStatus): string {
  return PROVIDER_STATUS_LABELS[status]
}

/**
 * The two states that count as "this provider can serve a turn right now"
 * (FR-019): the *Change default model* selector is filtered to exactly these.
 */
export const USABLE_PROVIDER_STATUSES: readonly ProviderStatus[] = ['connected', 'signed_in']

export function isProviderUsable(status: ProviderStatus): boolean {
  return USABLE_PROVIDER_STATUSES.includes(status)
}

/**
 * FR-016/MAJ-011: the Remove dialog's *New default model* selector offers
 * every OTHER configured provider — including `error` and `expired` rows,
 * which are the operator's risk to take — and excludes only
 * `unknown-provider`, whose model list is empty by construction (ADR-067
 * FR-016) so nothing there could be chosen.
 */
export function isEligibleNewDefault(status: ProviderStatus): boolean {
  return status !== 'unknown-provider'
}
