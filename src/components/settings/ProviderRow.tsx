import { CheckCircle, XCircle, Warning, CaretRight, Globe, SpinnerGap } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { BrandIcon } from '@/components/ui/brand-icon'
import { providerCatalogMode } from '@/lib/agents/providerCatalog'
import { ProviderValidationBanner } from '@/components/providers/ProviderValidationBanner'
import type { ProviderValidation, Provider } from '@/lib/api/generated/openapi-types'
import type { CatalogProvider } from '@/lib/api/generated/openapi-types'
import { catalogLogoSlug, catalogSubtitle } from '@/lib/catalogDisplay'

// See ProvidersSection.tsx's file header for the FIX-N legend. `entry` is the
// registry-fed CatalogProvider resolved for this configured row (ADR-068
// FR-037) — display strings derive from src/lib/catalogDisplay.ts.

// ADR-068 FR-005/FR-045: a row whose catalog entry declares `sign_in` in
// `auth_methods` renders a Sign in / Signed-in-as / Sign in again control
// (SignInDialog, T068-33) INSTEAD of the API-key Configure button. Gated
// primarily on `entry.auth_methods` (the literal FR-005 signal — the
// registry-fed CatalogProvider, `GET /providers/catalog`); falls back to the
// configured Provider row's own `auth_method` field (Provider.yaml, always
// present) for the rare case a row's `entry` failed to resolve (e.g. a
// catalog refresh gap) but the row itself already carries a sign_in config.
// The served catalog carries three such rows today — `openai-chatgpt`,
// `codex-cli` and `github-copilot`; `xai` stays key-only until its row
// carries `sign_in` (FR-049), and Anthropic/Google never gain it
// (ADR-068 §8b decision 4).
function isSignInCapable(provider: Provider, entry?: CatalogProvider): boolean {
  if (entry) return entry.auth_methods.includes('sign_in')
  return provider.auth_method === 'sign_in'
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
  /** Opens the SignInDialog for this row (sign-in-capable rows only). */
  onSignIn?: () => void
  /** Fires the sign-out mutation for this row (sign-in-capable rows only). */
  onSignOut?: () => void
  /** True while the SignInDialog is open/polling for this row's provider id. */
  signingIn?: boolean
  /** True while a sign-out mutation is in flight (any row — the mutation is not per-id). */
  signingOut?: boolean
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
  onSignOut,
  signingIn,
  signingOut,
  testValidation,
  isDefault = false,
  onSetAsDefault,
}: ProviderRowProps) {
  const connected = provider.status === 'connected'
  const signInCapable = isSignInCapable(provider, entry)
  const catalogMode = providerCatalogMode(provider)
  const subtitle = entry ? catalogSubtitle(entry) : undefined

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
            {signInCapable ? (
              provider.status === 'signed_in' ? (
                <Badge data-testid={`signed-in-badge-${provider.id}`} variant="success" className="gap-1">
                  <CheckCircle size={10} weight="fill" />
                  {provider.account_label ? `Signed in as ${provider.account_label}` : 'Signed in'}
                </Badge>
              ) : provider.status === 'expired' ? (
                <Badge data-testid={`expired-badge-${provider.id}`} variant="warning" className="gap-1">
                  <Warning size={10} weight="fill" /> Session expired
                </Badge>
              ) : provider.status === 'error' ? (
                <Badge variant="error" className="gap-1" data-testid={`error-badge-${provider.id}`}>
                  <XCircle size={10} weight="fill" /> Error
                </Badge>
              ) : signingIn ? (
                <Badge variant="muted" className="gap-1" data-testid={`pending-badge-${provider.id}`}>
                  <SpinnerGap size={10} className="animate-spin" /> Signing in…
                </Badge>
              ) : (
                <Badge variant="muted" data-testid={`not-signed-in-badge-${provider.id}`}>
                  Not signed in
                </Badge>
              )
            ) : connected ? (
              <Badge data-testid={`connected-badge-${provider.id}`} variant="success" className="gap-1">
                <CheckCircle size={10} weight="fill" /> Connected
              </Badge>
            ) : provider.status === 'error' ? (
              <Badge
                variant="error"
                className="gap-1"
                data-testid={`error-badge-${provider.id}`}
              >
                <XCircle size={10} weight="fill" /> Error
              </Badge>
            ) : (
              <Badge variant="muted" data-testid={`disconnected-badge-${provider.id}`}>
                Not configured
              </Badge>
            )}
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
            provider.status === 'signed_in' ? (
              <Button
                size="sm"
                variant="outline"
                onClick={onSignOut}
                disabled={signingOut}
                className="h-7 px-3 text-xs"
                data-testid={`sign-out-btn-${provider.id}`}
              >
                Sign out
              </Button>
            ) : (
              <Button
                size="sm"
                onClick={onSignIn}
                disabled={signingIn}
                className="h-7 px-3 text-xs"
                data-testid={`sign-in-btn-${provider.id}`}
              >
                {provider.status === 'expired' ? 'Sign in again' : 'Sign in'}
              </Button>
            )
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
