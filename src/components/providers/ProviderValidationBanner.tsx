// ProviderValidationBanner — inline banner for non-'valid' provider validation
// outcomes. Amber for non-blocking warnings (NoCredit / Unreachable / Restricted);
// RED for the blocking `invalid_key` outcome (ADR-031 silent-failure fix — the
// contract says invalid_key blocks a usable provider, so it must not vanish).
//
// Spec: provider-validation-centralization-spec.md, US8 / D3 / R-H/m2.
//
// Decision D3: per-outcome Phosphor icon; copy is the server-provided `message`
// (single source of truth — never hardcoded here). Valid/absent → renders
// nothing; any other present outcome always renders (default Warning icon).
//
// Icon assignment:
//   invalid_key → XCircle (red/error)
//   no_credit   → Wallet
//   unreachable → WifiSlash
//   restricted  → Lock
//   (unknown)   → Warning

import { Wallet, WifiSlash, Lock, XCircle, Warning } from '@phosphor-icons/react'
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

  // invalid_key is a BLOCKING outcome — render it red (error), not amber.
  const isBlocking = outcome === 'invalid_key'

  const icon = (() => {
    switch (outcome) {
      case 'invalid_key':
        return <XCircle size={14} weight="fill" className="shrink-0 mt-0.5" data-testid="banner-icon-x-circle" />
      case 'no_credit':
        return <Wallet size={14} weight="fill" className="shrink-0 mt-0.5" data-testid="banner-icon-wallet" />
      case 'unreachable':
        return <WifiSlash size={14} weight="fill" className="shrink-0 mt-0.5" data-testid="banner-icon-wifi-slash" />
      case 'restricted':
        return <Lock size={14} weight="fill" className="shrink-0 mt-0.5" data-testid="banner-icon-lock" />
      default:
        // Never return null for a present, non-valid outcome — a new contract
        // outcome must still render its server message rather than vanishing.
        return <Warning size={14} weight="fill" className="shrink-0 mt-0.5" data-testid="banner-icon-warning" />
    }
  })()

  return (
    <div
      role={isBlocking ? 'alert' : 'status'}
      aria-live={isBlocking ? 'assertive' : 'polite'}
      data-testid={testId}
      data-outcome={outcome}
      className={
        isBlocking
          ? 'flex items-start gap-2 rounded-lg border border-red-500/40 bg-red-500/10 px-3 py-2.5 text-sm text-red-300'
          : 'flex items-start gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2.5 text-sm text-amber-300'
      }
    >
      {icon}
      <span>{message ?? outcomeDefaultCopy(outcome)}</span>
    </div>
  )
}

/** Fallback copy when the server omits the message (should not happen in practice). */
function outcomeDefaultCopy(outcome: ProviderValidation['outcome']): string {
  switch (outcome) {
    case 'invalid_key':
      return 'The key was rejected — the provider will not work until you enter a valid key.'
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
