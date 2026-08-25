import type { ReactNode } from 'react'
import { CheckCircle, XCircle, Warning, CaretRight, Circle, Globe, Question, SpinnerGap } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { BrandIcon } from '@/components/ui/brand-icon'
import { providerCatalogMode } from '@/lib/agents/providerCatalog'
import { ProviderValidationBanner } from '@/components/providers/ProviderValidationBanner'
import { providerStatusLabel, type ProviderStatus } from '@/lib/providerStatus'
import type { ProviderValidation, Provider } from '@/lib/api/generated/openapi-types'
import type { CatalogProvider } from '@/lib/api/generated/openapi-types'
import { catalogLogoSlug, catalogSubtitle } from '@/lib/catalogDisplay'

// See ProvidersSection.tsx's file header for the FIX-N legend. `entry` is the
// registry-fed CatalogProvider resolved for this configured row (ADR-068
// FR-037) — display strings derive from src/lib/catalogDisplay.ts.

// ADR-068 FR-005/FR-045: a row whose catalog entry declares `sign_in` in
// `auth_methods` renders a Sign in / Signed-in-as / Manage control
// (SignInDialog, T068-33; Manage/re-sign-in, T068-26) INSTEAD of the API-key
// Configure button. Gated primarily on `entry.auth_methods` (the literal
// FR-005 signal — the registry-fed CatalogProvider, `GET /providers/catalog`);
// falls back to the configured Provider row's own `auth_method` field
// (Provider.yaml, always present) for the rare case a row's `entry` failed to
// resolve (e.g. a catalog refresh gap) but the row itself already carries a
// sign_in config. The served catalog carries three such rows today —
// `openai-chatgpt`, `codex-cli` and `github-copilot`; `xai` stays key-only
// until its row carries `sign_in` (FR-049), and Anthropic/Google never gain
// it (ADR-068 §8b decision 4).
function isSignInCapable(provider: Provider, entry?: CatalogProvider): boolean {
  if (entry) return entry.auth_methods.includes('sign_in')
  return provider.auth_method === 'sign_in'
}

// cliKindOf resolves the `cli_login` subprocess driver for a sign-in row —
// the catalog entry first (ADR-067 X-14), falling back to the configured
// row's own `cli_kind` for the same "entry failed to resolve" gap
// `isSignInCapable` guards against. `undefined` means this is a
// `device_code` row (openai-chatgpt / xai) rather than `cli_login`
// (codex-cli / github-copilot) — the distinction the Manage action needs to
// pick a re-sign-in surface (T068-26): `cli_login` rows never need Omnipus
// to refresh anything (FR-007), so an expired session re-checks the vendor
// CLI's own saved login file (ReSignInDialog); `device_code` rows genuinely
// need a fresh device-code approval (SignInDialog) once expired.
function cliKindOf(provider: Provider, entry?: CatalogProvider): 'codex' | 'copilot' | undefined {
  return entry?.cli_kind ?? provider.cli_kind
}

// ---------------------------------------------------------------------------
// FR-038: Provider.status is one closed six-value enum. Every consumer that
// switches on it — this badge spec and the sign-in row's action label below —
// uses an exhaustive `switch` with a `never` default so a seventh status
// value fails typecheck instead of silently falling through to the wrong
// copy (T068-26 DoD).
// ---------------------------------------------------------------------------

function assertNeverStatus(status: never): never {
  throw new Error(`unhandled provider status: ${String(status)}`)
}

interface BadgeSpec {
  testId: string
  variant: 'success' | 'warning' | 'error' | 'muted'
  icon: ReactNode
  text: string
}

// statusBadge renders icon + text for every one of the six statuses — never
// colour alone (T068-26 DoD). `signInCapable` only changes the copy for
// `disconnected` (a sign-in row that has never signed in reads "Not signed
// in", not "Not configured") and adds the transient `signingIn` pending
// state; every other status reads identically regardless of auth method.
function statusBadge(
  status: ProviderStatus,
  providerId: string,
  opts: { signInCapable: boolean; accountLabel?: string; signingIn?: boolean },
): BadgeSpec {
  switch (status) {
    case 'signed_in':
      return {
        testId: `signed-in-badge-${providerId}`,
        variant: 'success',
        icon: <CheckCircle size={10} weight="fill" />,
        // BDD "Signed-in row shows account and Manage": "Signed in · <label>"
        // with the label, "Signed in" without one.
        text: opts.accountLabel ? `Signed in · ${opts.accountLabel}` : 'Signed in',
      }
    case 'expired':
      return {
        testId: `expired-badge-${providerId}`,
        variant: 'warning',
        icon: <Warning size={10} weight="fill" />,
        text: providerStatusLabel('expired'),
      }
    case 'error':
      return {
        testId: `error-badge-${providerId}`,
        variant: 'error',
        icon: <XCircle size={10} weight="fill" />,
        text: providerStatusLabel('error'),
      }
    case 'unknown-provider':
      return {
        testId: `unknown-provider-badge-${providerId}`,
        variant: 'error',
        icon: <Question size={10} weight="fill" />,
        text: providerStatusLabel('unknown-provider'),
      }
    case 'connected':
      return {
        testId: `connected-badge-${providerId}`,
        variant: 'success',
        icon: <CheckCircle size={10} weight="fill" />,
        text: providerStatusLabel('connected'),
      }
    case 'disconnected':
      if (opts.signInCapable) {
        return opts.signingIn
          ? {
              testId: `pending-badge-${providerId}`,
              variant: 'muted',
              icon: <SpinnerGap size={10} className="animate-spin" />,
              text: 'Signing in…',
            }
          : {
              testId: `not-signed-in-badge-${providerId}`,
              variant: 'muted',
              icon: <Circle size={10} />,
              text: 'Not signed in',
            }
      }
      return {
        testId: `disconnected-badge-${providerId}`,
        variant: 'muted',
        icon: <Circle size={10} />,
        text: providerStatusLabel('disconnected'),
      }
    default:
      return assertNeverStatus(status)
  }
}

// signInActionLabel is the primary action word for a sign-in-capable row
// (FR-034): "Sign in" for a row that has never connected (or errored) and
// "Manage" for one that has — signed in, or previously signed in and now
// expired. Non-sign-in rows never reach this function (they use
// Edit/Configure, computed inline below).
function signInActionLabel(status: ProviderStatus): 'Sign in' | 'Manage' {
  switch (status) {
    case 'signed_in':
    case 'expired':
      return 'Manage'
    case 'disconnected':
    case 'error':
      return 'Sign in'
    // A sign-in row is never `connected`/`unknown-provider` in practice —
    // those statuses belong to API-key rows — but the switch must stay
    // exhaustive over all six wire values (FR-038 DoD).
    case 'connected':
    case 'unknown-provider':
      return 'Manage'
    default:
      return assertNeverStatus(status)
  }
}

// ---------------------------------------------------------------------------
// Sub-component: a single configured-provider row (flat or inside a group)
// ---------------------------------------------------------------------------

export interface ProviderRowProps {
  provider: Provider
  entry?: CatalogProvider
  title: string
  showIcon: boolean
  onConfigure: () => void
  /** Opens the SignInDialog for this row (sign-in-capable, not-yet-connected rows only). */
  onSignIn?: () => void
  /**
   * ADR-068 FR-034: opens the Manage surface for a sign-in-capable row that
   * has connected at least once — the account/sign-out view for `signed_in`,
   * or the re-sign-in flow for `expired` (the caller picks ManageSignInDialog
   * vs. ReSignInDialog vs. the fresh SignInDialog based on `provider.status`
   * and `cli_kind` — see ProvidersSection.tsx's `handleManage`).
   */
  onManage?: () => void
  /** True while the SignInDialog is open/polling for this row's provider id. */
  signingIn?: boolean
  testValidation?: ProviderValidation
  /**
   * ADR-068 FR-019: this row backs `agents.defaults.default_model`. Derived by
   * the caller from the default-model GET — never from a per-row wire field.
   */
  isDefault?: boolean
  /**
   * FR-019's row action. Omitted → the action is absent (a row whose provider
   * cannot serve a turn has nothing to make default).
   */
  onSetAsDefault?: () => void
}

export function ProviderRow({
  provider,
  entry,
  title,
  showIcon,
  onConfigure,
  onSignIn,
  onManage,
  signingIn,
  testValidation,
  isDefault = false,
  onSetAsDefault,
}: ProviderRowProps) {
  const connected = provider.status === 'connected'
  const signInCapable = isSignInCapable(provider, entry)
  const catalogMode = providerCatalogMode(provider)
  const subtitle = entry ? catalogSubtitle(entry) : undefined
  const badge = statusBadge(provider.status, provider.id, {
    signInCapable,
    accountLabel: provider.account_label,
    signingIn,
  })
  // The Manage/Sign-in click routes to whichever handler the current status
  // calls for — ProvidersSection decides which dialog that opens.
  const handleSignInAction = signInActionLabel(provider.status) === 'Manage' ? onManage : onSignIn

  return (
    <div
      className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
      data-testid={`provider-row-${provider.id}`}
    >
      {/* Test-validation warning banner */}
      {testValidation && (
        <ProviderValidationBanner
          validation={testValidation}
          data-testid={`test-validation-banner-${provider.id}`}
        />
      )}

      <div className="flex items-center gap-3 px-4 py-3">
        {showIcon && (
          entry ? (
            <BrandIcon slug={catalogLogoSlug(entry)} size={22} decorative className="shrink-0" />
          ) : (
            <Globe size={22} className="text-[var(--color-muted)] shrink-0" aria-hidden="true" />
          )
        )}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span
              className="text-sm font-medium text-[var(--color-secondary)]"
              data-testid={`provider-row-title-${provider.id}`}
            >
              {title}
            </span>
            <Badge data-testid={badge.testId} variant={badge.variant} className="gap-1">
              {badge.icon}
              {badge.text}
            </Badge>
            {isDefault && (
              <Badge
                variant="muted"
                className="font-normal"
                data-testid={`default-badge-${provider.id}`}
              >
                Default
              </Badge>
            )}
            {!signInCapable && connected && (
              <Badge variant="muted" className="font-normal">
                {catalogMode === 'live' ? 'Live model list' : 'Manual models'}
              </Badge>
            )}
          </div>
          {subtitle && (
            <p className="text-xs text-[var(--color-muted)] mt-0.5">
              {subtitle}
            </p>
          )}
          {provider.models && provider.models.length > 0 && (
            <p className="text-xs text-[var(--color-muted)] mt-0.5 font-mono">
              {provider.models.slice(0, 3).join(', ')}{provider.models.length > 3 ? ` +${provider.models.length - 3}` : ''}
            </p>
          )}
          {provider.error && (
            <p className="text-xs text-[var(--color-error)] mt-0.5">{provider.error}</p>
          )}
        </div>

        <div className="flex items-center gap-2 shrink-0">
          {onSetAsDefault && (
            <button tabIndex={0}
              type="button"
              onClick={onSetAsDefault}
              className="text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
              data-testid={`set-default-btn-${provider.id}`}
            >
              Set as default model…
            </button>
          )}
          {signInCapable ? (
            <Button
              size="sm"
              variant={signInActionLabel(provider.status) === 'Manage' ? 'outline' : 'default'}
              onClick={handleSignInAction}
              disabled={signingIn}
              className="h-7 px-3 text-xs"
              data-testid={
                signInActionLabel(provider.status) === 'Manage'
                  ? `manage-btn-${provider.id}`
                  : `sign-in-btn-${provider.id}`
              }
            >
              {signInActionLabel(provider.status)}
            </Button>
          ) : (
            <Button
              size="sm"
              onClick={onConfigure}
              className="h-7 px-3 text-xs"
              data-testid={`configure-btn-${provider.id}`}
            >
              {connected ? 'Edit' : (
                <><CaretRight size={11} /> Configure</>
              )}
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}

export { isSignInCapable, cliKindOf }
