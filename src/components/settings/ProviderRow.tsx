import type { ReactNode } from 'react'
import { useState } from 'react'
import {
  CheckCircle,
  XCircle,
  Warning,
  CaretRight,
  CaretDown,
  Circle,
  Globe,
  Question,
  SpinnerGap,
  ArrowsClockwise,
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { BrandIcon } from '@/components/ui/brand-icon'
import { providerCatalogMode } from '@/lib/agents/providerCatalog'
import { ProviderValidationBanner } from '@/components/providers/ProviderValidationBanner'
import { providerStatusLabel, isProviderUsable, type ProviderStatus } from '@/lib/providerStatus'
import {
  MODEL_OVERRIDE_LINK_TEXT,
  NO_CONTEXT_LENGTH_COPY,
  modelOverrideHref,
} from '@/lib/modelOverrideLink'
import type { ProviderValidation, Provider, EntitlementResponse } from '@/lib/api/generated/openapi-types'
import type { CatalogProvider, CatalogModel } from '@/lib/api/generated/openapi-types'
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

// ADR-068 FR-031: "Check with my account" makes ONE live listing call with
// the provider's own key. The gateway 409s for protocol "cli" (codex-cli,
// github-copilot — there is no key to list with, only a subprocess) and for
// custom rows (no catalog entry to intersect against), so the control is
// never offered for either — clicking a button that is guaranteed to 409
// would just trade one dead end for another. Anything else that can serve a
// turn right now (connected or signed_in, ADR-068 FR-019's usable set) is
// offered the control.
export function isEntitlementEligible(provider: Provider): boolean {
  if (provider.custom) return false
  if (provider.protocol === 'cli') return false
  return isProviderUsable(provider.status)
}

/** What an absent numeric field renders as — same em dash the Default-model card uses. */
const ABSENT_FIELD = '—'
const TOKEN_FORMAT = new Intl.NumberFormat('en-US')

function formatTokenCount(n: number | undefined): string {
  return n !== undefined && n > 0 ? TOKEN_FORMAT.format(n) : ABSENT_FIELD
}

// One row of the expanded model-limits list (FR-032): either a catalog model
// (window/output/modality data available) or an entitlement-only model the
// provider reported that the catalog does not carry (BDD "Z" — limits always
// unknown by construction, since there is no catalog row to read them from).
interface ModelLimitRow {
  id: string
  name?: string
  contextWindow?: number
  maxOutputTokens?: number
  supportsImage?: boolean
  supportsPdf?: boolean
  windowSource?: CatalogModel['window_source']
  windowUnknown?: boolean
  /** undefined = no "Check with my account" result yet for this provider. */
  entitled?: boolean
  limitsUnknown?: boolean
}

// Merges the catalog entry's models with an (optional) entitlement result
// (FR-031/FR-032): every catalog model, annotated with its entitlement state
// when known, PLUS any model the entitlement check reported that the catalog
// does not carry (limits always "unknown" for those — there is nothing to
// read the limits from).
function buildModelLimitRows(entry: CatalogProvider | undefined, entitlement: EntitlementResponse | undefined): ModelLimitRow[] {
  const catalogModels = entry?.models ?? []
  const entitlementById = new Map((entitlement?.models ?? []).map((m) => [m.id, m]))
  const catalogIds = new Set(catalogModels.map((m) => m.id))

  const rows: ModelLimitRow[] = catalogModels.map((m) => {
    const ent = entitlementById.get(m.id)
    return {
      id: m.id,
      name: m.name,
      contextWindow: m.context_window,
      maxOutputTokens: m.max_output_tokens,
      supportsImage: m.input_modalities.includes('image'),
      supportsPdf: m.input_modalities.includes('pdf'),
      windowSource: m.window_source,
      windowUnknown: m.window_unknown === true,
      entitled: ent?.entitled,
      limitsUnknown: ent ? ent.limits === 'unknown' : undefined,
    }
  })

  const extraRows: ModelLimitRow[] = (entitlement?.models ?? [])
    .filter((m) => !catalogIds.has(m.id))
    .map((m) => ({ id: m.id, entitled: m.entitled, limitsUnknown: true }))

  return [...rows, ...extraRows]
}

function ModelLimitLine({ providerId, row }: { providerId: string; row: ModelLimitRow }) {
  const notEntitled = row.entitled === false
  return (
    <div
      className="flex flex-wrap items-center gap-x-3 gap-y-1 py-1.5 border-b border-[var(--color-border)] last:border-0"
      data-testid={`model-limit-row-${providerId}-${row.id}`}
    >
      <span
        className="text-xs font-mono text-[var(--color-secondary)] min-w-0 truncate"
        data-testid={`model-limit-id-${providerId}-${row.id}`}
      >
        {row.name ?? row.id}
      </span>
      {notEntitled && (
        // FR-031: greyed with the literal copy — plain --color-muted text
        // (never a reduced-opacity treatment), so the state stays ≥ 4.5:1
        // against the row background instead of washing out below AA.
        <Badge variant="muted" className="font-normal" data-testid={`model-limit-not-entitled-${providerId}-${row.id}`}>
          not available on this key
        </Badge>
      )}
      {row.limitsUnknown === true && (
        <Badge variant="muted" className="font-normal" data-testid={`model-limit-limits-unknown-${providerId}-${row.id}`}>
          limits unknown
        </Badge>
      )}
      <div className="flex flex-wrap items-center gap-x-2 text-xs text-[var(--color-muted)]">
        {row.windowUnknown ? (
          <>
            <span data-testid={`model-limit-window-${providerId}-${row.id}`}>{NO_CONTEXT_LENGTH_COPY}</span>
            <a
              tabIndex={0}
              href={modelOverrideHref(providerId, row.id)}
              data-testid={`model-limit-window-unknown-link-${providerId}-${row.id}`}
              className="underline underline-offset-2"
              style={{ color: 'var(--color-accent)' }}
            >
              {MODEL_OVERRIDE_LINK_TEXT}
            </a>
          </>
        ) : (
          <>
            <span data-testid={`model-limit-window-${providerId}-${row.id}`}>{formatTokenCount(row.contextWindow)}</span>
            <span aria-hidden="true">·</span>
            <span data-testid={`model-limit-output-${providerId}-${row.id}`}>{formatTokenCount(row.maxOutputTokens)}</span>
            <span aria-hidden="true">·</span>
            <span data-testid={`model-limit-image-${providerId}-${row.id}`}>{row.supportsImage ? 'Image' : ABSENT_FIELD}</span>
            <span aria-hidden="true">·</span>
            <span data-testid={`model-limit-pdf-${providerId}-${row.id}`}>{row.supportsPdf ? 'PDF' : ABSENT_FIELD}</span>
            <span aria-hidden="true">·</span>
            <span data-testid={`model-limit-source-${providerId}-${row.id}`}>{row.windowSource ?? ABSENT_FIELD}</span>
          </>
        )}
      </div>
    </div>
  )
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
  /**
   * ADR-068 FR-031/T068-27: "Check with my account". Fires the entitlement
   * check owned by ProvidersSection (queryClient lives there, so an in-flight
   * result can be discarded if the row's provider is deleted before it
   * lands). Undefined when the provider is not entitlement-eligible
   * (isEntitlementEligible) — the control is omitted entirely rather than
   * rendered disabled, matching the row's other conditional actions.
   */
  onCheckEntitlement?: () => void
  /** True while the entitlement mutation for this provider id is in flight. */
  checkingEntitlement?: boolean
  /** The last entitlement result for this provider id, if any. */
  entitlement?: EntitlementResponse
  /** FR-031's failure path: "leaves the list unchanged... with an inline warning". */
  entitlementError?: string
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
  onCheckEntitlement,
  checkingEntitlement = false,
  entitlement,
  entitlementError,
}: ProviderRowProps) {
  const [expanded, setExpanded] = useState(false)
  const connected = provider.status === 'connected'
  const signInCapable = isSignInCapable(provider, entry)
  const catalogMode = providerCatalogMode(provider)
  const subtitle = entry ? catalogSubtitle(entry) : undefined
  const limitRows = buildModelLimitRows(entry, entitlement)
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
            <button
              tabIndex={0}
              type="button"
              onClick={() => setExpanded((v) => !v)}
              aria-expanded={expanded}
              aria-controls={`model-limits-${provider.id}`}
              className="flex items-center gap-1 text-sm font-medium text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
              data-testid={`provider-row-expand-toggle-${provider.id}`}
            >
              <CaretDown
                size={12}
                className={`shrink-0 transition-transform duration-150 text-[var(--color-muted)] ${expanded ? 'rotate-0' : '-rotate-90'}`}
                aria-hidden="true"
              />
              <span data-testid={`provider-row-title-${provider.id}`}>{title}</span>
            </button>
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
          {onCheckEntitlement && (
            <button
              tabIndex={0}
              type="button"
              onClick={onCheckEntitlement}
              disabled={checkingEntitlement}
              className="flex items-center gap-1 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors disabled:opacity-50"
              data-testid={`check-entitlement-btn-${provider.id}`}
            >
              {checkingEntitlement ? (
                <ArrowsClockwise size={12} className="animate-spin" />
              ) : null}
              Check with my account
            </button>
          )}
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

      {/* FR-031 inline warning — the list stays unchanged (nothing greyed) on
          an upstream failure; this is the only visible effect. */}
      {entitlementError && (
        <p
          role="alert"
          aria-live="assertive"
          className="px-4 pb-2 text-xs text-[var(--color-error)]"
          data-testid={`entitlement-error-${provider.id}`}
        >
          {entitlementError}
        </p>
      )}

      {/* FR-032 row expand: catalog window · output · image · PDF and the
          window-source cell, annotated with the last "Check with my
          account" result for this provider (if any). */}
      {expanded && (
        <div
          id={`model-limits-${provider.id}`}
          className="border-t border-[var(--color-border)] px-4 py-2 bg-[var(--color-surface-2)]"
          data-testid={`model-limits-${provider.id}`}
        >
          {limitRows.length > 0 ? (
            limitRows.map((row) => (
              <ModelLimitLine key={row.id} providerId={provider.id} row={row} />
            ))
          ) : (
            <p className="text-xs text-[var(--color-muted)] py-1.5" data-testid={`model-limits-empty-${provider.id}`}>
              No catalog data available for this provider.
            </p>
          )}
        </div>
      )}
    </div>
  )
}

export { isSignInCapable, cliKindOf }
