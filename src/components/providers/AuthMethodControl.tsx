// AuthMethodControl.tsx — ADR-068 FR-005, FR-006 (amended §8b), FR-028.
//
// The auth-method choice lives INSIDE the picker's second-level panel, beside
// plan and region — never as a fourth onboarding step (FR-028). Switching the
// segment reveals the key field or the sign-in options in place, with no
// navigation, which is what keeps the three-step tracker at three steps.
//
// Two rules this component exists to enforce:
//
//  1. FR-005 — a sign-in control appears ONLY where the catalog row's
//     `auth_methods` contains `sign_in`, and where it appears it is
//     pre-selected. `signInOptions` is therefore derived from catalog data by
//     the caller; an empty list means the sign-in half is absent from the DOM
//     entirely, not merely disabled. That absence is the tested guarantee for
//     Anthropic and Google (ADR-068 §8b decision 4) and for xAI until its row
//     carries `sign_in` (FR-049) — no placeholder, no "coming soon" copy.
//
//  2. FR-006 as amended 2026-08-23 (ADR-068 §8b decision 2) — where a vendor
//     offers more than one sign-in provider, they are radio options in CATALOG
//     ORDER and the FIRST is pre-selected. For OpenAI the catalog lists
//     `openai-chatgpt` before `codex-cli`, so the in-app device-code login is
//     the default and the CLI subprocess is the alternative. The withdrawn
//     "relies on OpenAI's stated tolerance" caveat is not in this file, and
//     `AuthMethodControl.test.tsx` asserts it appears nowhere in the rendered
//     DOM.
//
// The *Sign in* button is a seam, not a flow: it calls `onSignIn` with the
// selected provider id. T068-33's `SignInDialog` (device code / CLI login)
// dropped into that callback with no change here — every caller that wants a
// working sign-in supplies `onSignIn` and renders that dialog itself
// (`ProviderDetailPanel` → `ProviderPicker` → onboarding step 3 and
// Settings → Providers), so this file stays free of any transport concern.

import * as React from 'react'
import { Key, SignIn } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { SegmentedControl, SegmentedControlItem } from '@/components/ui/segmented-control'

/** The closed auth-method set (contract `Provider.auth_method`, X-25). */
export type AuthMethod = 'api_key' | 'sign_in'

/**
 * FR-006 / §5 item 2 helper copy, keyed by provider id. Copy this spec owns;
 * the label itself is the catalog row's `name`, never a SPA constant.
 *
 * A provider with no entry here simply renders no helper line — a new
 * `sign_in` row in the catalog is usable the day it is served.
 */
export const SIGN_IN_HELPER_COPY: Readonly<Record<string, string>> = Object.freeze({
  'openai-chatgpt': "Uses your ChatGPT plan's included usage",
  'codex-cli': 'Drives the official Codex app; sign in inside it',
  'github-copilot': 'Billed to your Copilot subscription',
})

/** Helper line for a sign-in provider, or undefined when the spec names none. */
export function signInHelperCopy(providerId: string): string | undefined {
  return SIGN_IN_HELPER_COPY[providerId]
}

/** One sign-in provider offered for the selected company, in catalog order. */
export interface SignInOption {
  /** The catalog provider id persisted on save (e.g. `openai-chatgpt`). */
  providerId: string
  /** Display label — the catalog row's `name`. */
  label: string
  /** FR-006 helper line. Defaults to `signInHelperCopy(providerId)`. */
  helper?: string
}

export interface AuthMethodControlProps {
  /**
   * Sign-in providers this company offers, catalog order. Empty or omitted →
   * no sign-in control anywhere in the DOM (FR-005).
   */
  signInOptions?: readonly SignInOption[]
  /** False only for a company with no `api_key` variant at all. Default true. */
  apiKeyOffered?: boolean
  /** Company display name — the segment reads *Sign in with `<company>`*. */
  companyName?: string
  /** Initial method. Default: `sign_in` when offered (FR-005), else `api_key`. */
  defaultMethod?: AuthMethod
  /** Initial sign-in provider. Default: the FIRST option (FR-006). */
  defaultSignInProviderId?: string
  /** Fired on every method change, and once on mount with the resolved default. */
  onMethodChange?: (method: AuthMethod) => void
  /** Fired on every sign-in provider change, and once on mount with the default. */
  onSignInProviderChange?: (providerId: string) => void
  /**
   * Opens the sign-in dialog for the selected provider (T068-33). A caller
   * that omits it leaves the button inert — the control still renders, which
   * is what keeps the panel's layout and focus order identical either way.
   */
  onSignIn?: (providerId: string) => void
  /** Rendered under the segment while the API-key method is selected. */
  apiKeyField?: React.ReactNode
  'data-testid'?: string
}

export function AuthMethodControl({
  signInOptions,
  apiKeyOffered = true,
  companyName,
  defaultMethod,
  defaultSignInProviderId,
  onMethodChange,
  onSignInProviderChange,
  onSignIn,
  apiKeyField,
  'data-testid': testId = 'auth-method-control',
}: AuthMethodControlProps) {
  const options = React.useMemo(() => signInOptions ?? [], [signInOptions])
  const signInOffered = options.length > 0

  // FR-005: pre-selected where present. A caller-supplied default still wins,
  // but a default naming a method the company does not offer is ignored rather
  // than rendering a segment with nothing behind it.
  const resolvedDefaultMethod: AuthMethod =
    defaultMethod === 'sign_in' && signInOffered
      ? 'sign_in'
      : defaultMethod === 'api_key' && apiKeyOffered
        ? 'api_key'
        : signInOffered
          ? 'sign_in'
          : 'api_key'

  // FR-006: the first option in catalog order is the default.
  const resolvedDefaultProvider =
    (defaultSignInProviderId &&
      options.find((o) => o.providerId === defaultSignInProviderId)?.providerId) ||
    options[0]?.providerId ||
    ''

  const [method, setMethod] = React.useState<AuthMethod>(resolvedDefaultMethod)
  const [providerId, setProviderId] = React.useState<string>(resolvedDefaultProvider)

  // Announce the resolved defaults once, so a caller that never touches the
  // control still knows what a save would persist (the DoD's "saving with
  // OpenAI sign-in untouched persists openai-chatgpt").
  const announced = React.useRef(false)
  React.useEffect(() => {
    if (announced.current) return
    announced.current = true
    onMethodChange?.(resolvedDefaultMethod)
    if (resolvedDefaultProvider) onSignInProviderChange?.(resolvedDefaultProvider)
  }, [onMethodChange, onSignInProviderChange, resolvedDefaultMethod, resolvedDefaultProvider])

  const chooseMethod = (next: AuthMethod) => {
    if (next === method) return
    setMethod(next)
    onMethodChange?.(next)
  }

  const chooseProvider = (next: string) => {
    if (next === providerId) return
    setProviderId(next)
    onSignInProviderChange?.(next)
  }

  const signInLabel = companyName ? `Sign in with ${companyName}` : 'Sign in'
  const radioGroupLabelId = `${testId}-signin-label`

  return (
    <div data-testid={testId} className="flex flex-col gap-[var(--space-2)]">
      {/* The segment exists only when there is a real choice: one method needs
          no control, and a single-button "segmented control" is a lie about
          what the operator can do. */}
      {signInOffered && apiKeyOffered && (
        <SegmentedControl
          aria-label="Authentication method"
          value={method}
          onValueChange={(next) => chooseMethod(next as AuthMethod)}
          data-testid={`${testId}-segment`}
          className="flex w-full gap-[var(--space-1)] p-[var(--space-1)]"
        >
          <SegmentedControlItem
            value="sign_in"
            data-testid={`${testId}-segment-sign_in`}
            className="h-auto min-h-[32px] min-w-0 flex-1 gap-[var(--space-1)] rounded px-[var(--space-2-5)] text-[length:var(--type-body-compact-size)]"
          >
            <SignIn size={14} aria-hidden="true" />
            {signInLabel}
          </SegmentedControlItem>
          <SegmentedControlItem
            value="api_key"
            data-testid={`${testId}-segment-api_key`}
            className="h-auto min-h-[32px] min-w-0 flex-1 gap-[var(--space-1)] rounded px-[var(--space-2-5)] text-[length:var(--type-body-compact-size)]"
          >
            <Key size={14} aria-hidden="true" />
            API key
          </SegmentedControlItem>
        </SegmentedControl>
      )}

      {signInOffered && method === 'sign_in' && (
        <div data-testid={`${testId}-signin`} className="flex flex-col gap-[var(--space-2)]">
          {/* One sign-in provider needs no radio group — the choice is made. */}
          {options.length > 1 && (
            <>
              <span
                id={radioGroupLabelId}
                className="text-[length:var(--type-utility-xs-size)] uppercase"
                style={{ color: 'var(--color-muted)' }}
              >
                Sign-in method
              </span>
              <div
                role="radiogroup"
                aria-labelledby={radioGroupLabelId}
                data-testid={`${testId}-signin-options`}
                className="flex flex-col gap-[var(--space-1)]"
              >
                {options.map((option) => {
                  const helper = option.helper ?? signInHelperCopy(option.providerId)
                  const inputId = `${testId}-signin-${option.providerId}`
                  const helperId = helper ? `${inputId}-helper` : undefined
                  return (
                    <Label
                      key={option.providerId}
                      htmlFor={inputId}
                      data-testid={`${testId}-signin-option-${option.providerId}`}
                      className="flex cursor-pointer items-start gap-[var(--space-2)] rounded border p-[var(--space-2)]"
                      style={{ borderColor: 'var(--color-border)' }}
                    >
                      <input
                        id={inputId}
                        type="radio"
                        tabIndex={0}
                        name={`${testId}-signin-provider`}
                        value={option.providerId}
                        checked={providerId === option.providerId}
                        aria-describedby={helperId}
                        onChange={() => chooseProvider(option.providerId)}
                        className="mt-[var(--space-1)]"
                      />
                      <span className="flex flex-col">
                        <span style={{ color: 'var(--color-secondary)' }}>{option.label}</span>
                        {helper && (
                          <span
                            id={helperId}
                            className="text-[length:var(--type-utility-xs-size)]"
                            style={{ color: 'var(--color-muted)' }}
                          >
                            {helper}
                          </span>
                        )}
                      </span>
                    </Label>
                  )
                })}
              </div>
            </>
          )}

          {options.length === 1 && options[0] && (
            <span className="text-[length:var(--type-utility-xs-size)]" style={{ color: 'var(--color-muted)' }}>
              {options[0].helper ?? signInHelperCopy(options[0].providerId) ?? options[0].label}
            </span>
          )}

          <Button
            type="button"
            variant="outline"
            data-testid={`${testId}-signin-start`}
            onClick={() => onSignIn?.(providerId)}
            className="h-auto min-h-[32px] gap-[var(--space-1)] rounded px-[var(--space-2-5)] text-[length:var(--type-body-compact-size)]"
          >
            <SignIn size={14} aria-hidden="true" />
            Sign in
          </Button>
        </div>
      )}

      {method === 'api_key' && apiKeyField && (
        <div data-testid={`${testId}-api-key`}>{apiKeyField}</div>
      )}
    </div>
  )
}
