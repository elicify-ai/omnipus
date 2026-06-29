// ProviderValidationBanner — amber warning banner for non-blocking provider
// validation outcomes (NoCredit / Unreachable / Restricted).
//
// Spec: provider-validation-centralization-spec.md, US8 / D3 / R-H/m2.
//
// Decision D3: one amber banner style; per-outcome Phosphor icon; copy is the
// server-provided `message` (single source of truth — never hardcoded here).
// Valid and absent outcomes render nothing.
//
// Icon assignment:
//   no_credit   → Wallet
//   unreachable → WifiSlash
//   restricted  → Lock

import { Wallet, WifiSlash, Lock } from '@phosphor-icons/react'
import type { ProviderValidation } from '@/lib/api/generated/openapi-types'

export interface ProviderValidationBannerProps {
  /** The validation object from the server. Absent or valid → renders nothing. */
  validation: ProviderValidation | undefined | null
  /** Optional data-testid override for the wrapper element. */
  'data-testid'?: string
}

export function ProviderValidationBanner({
  validation,
  'data-testid': testId = 'provider-validation-banner',
}: ProviderValidationBannerProps) {
  if (!validation || validation.outcome === 'valid') return null

  const { outcome, message } = validation

  const icon = (() => {
    switch (outcome) {
      case 'no_credit':
        return <Wallet size={14} weight="fill" className="shrink-0 mt-0.5" data-testid="banner-icon-wallet" />
      case 'unreachable':
        return <WifiSlash size={14} weight="fill" className="shrink-0 mt-0.5" data-testid="banner-icon-wifi-slash" />
      case 'restricted':
        return <Lock size={14} weight="fill" className="shrink-0 mt-0.5" data-testid="banner-icon-lock" />
      default:
        return null
    }
  })()

  if (!icon) return null

  return (
    <div
      role="status"
      aria-live="polite"
      data-testid={testId}
      data-outcome={outcome}
      className="flex items-start gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2.5 text-sm text-amber-300"
    >
      {icon}
      <span>{message ?? outcomeDefaultCopy(outcome)}</span>
    </div>
  )
}

/** Fallback copy when the server omits the message (should not happen in practice). */
function outcomeDefaultCopy(outcome: ProviderValidation['outcome']): string {
  switch (outcome) {
    case 'no_credit':
      return 'The key works but the account has insufficient credit.'
    case 'unreachable':
      return "Couldn't reach the provider to verify the key. Continuing with the key as entered."
    case 'restricted':
      return 'The key works but the request was blocked — the model may not be available in your region.'
    default:
      return 'The key was accepted with a warning.'
  }
}
