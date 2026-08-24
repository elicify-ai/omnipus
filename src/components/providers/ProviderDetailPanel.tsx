// ProviderDetailPanel.tsx — ADR-068 FR-027, FR-028, FR-005/FR-006: the picker's
// second-level panel.
//
// Picking a company in the first-level list settles the vendor; it does not
// settle WHICH row of that vendor gets configured. A Chinese vendor ships four
// rows that differ only by plan x region, and OpenAI ships an API-key row
// beside its sign-in rows. This panel is where that choice is made — plan,
// region, auth method — and it is deliberately part of the same step: adding a
// fourth onboarding step for it is what FR-028 forbids.
//
// Three properties worth stating, because they are what the tests pin:
//
//  1. Region is never a cold row of equal buttons (FR-027, §5 item 3). The
//     browser locale pre-selects one through `inferRegionFromLocale`, and the
//     copy says out loud that it was a guess — *"Detected: China — change"*, or
//     *"Region — change"* when nothing was inferred. The operator overriding it
//     is one click, and the word *change* is there to say so.
//
//  2. Plan and region are `aria-pressed` button groups, not selects: the option
//     count is tiny and fixed, and the current value must be readable without
//     opening anything. Exactly one button in each group is pressed at all
//     times — a group with no pressed button would mean "no plan chosen", a
//     state this panel never has.
//
//  3. Which auth methods exist is CATALOG data, never a SPA branch. The sign-in
//     options are the company's own `sign_in` variants in catalog order, so
//     Anthropic and Google render no sign-in control at all (ADR-068 §8b
//     decision 4) and xAI gains one the day its catalog row carries `sign_in`
//     (FR-049) with no code change here. Where a vendor's rows are grouped
//     under one company — as the served catalog groups OpenAI's — the two
//     sign-in rows become the FR-006 radio pair with the first, `openai-chatgpt`,
//     pre-selected.

import * as React from 'react'
import { AuthMethodControl, type AuthMethod, type SignInOption } from './AuthMethodControl'
import type { PickerCompanyRow } from './provider-picker-model'
import { inferRegionFromLocale, regionLabel } from './region-inference'

/** What the panel emits on *Continue* — one configurable provider row. */
export interface ProviderDetailSelection {
  /**
   * The provider id to persist: the sign-in provider when the sign-in method is
   * selected, otherwise the plan x region variant the two groups resolve to.
   */
  providerId: string
  authMethod: AuthMethod
  /** The chosen plan label, when the company has plan variants. */
  plan?: string
  /** The chosen region code, when the company has region variants. */
  region?: string
  /** The typed key — present only for `api_key`, never persisted by this panel. */
  apiKey?: string
}

export interface ProviderDetailPanelProps {
  /** The company row selected in the first-level list, with its variants. */
  company: PickerCompanyRow
  /**
   * Locale driving the region default (FR-027). Defaults to
   * `navigator.language`; passed explicitly by tests and by any caller that
   * knows better than the browser.
   */
  locale?: string | null
  /** Fired on *Continue* with the resolved selection. */
  onConfirm?: (selection: ProviderDetailSelection) => void
  /** Fired on every change, so a caller can keep a live draft. */
  onChange?: (selection: ProviderDetailSelection) => void
  onCancel?: () => void
  /**
   * T068-33 seam — opens `SignInDialog` for the given provider id. Until that
   * task lands the *Sign in* button is inert rather than absent, so the panel's
   * layout and focus order are already the final ones.
   */
  onSignIn?: (providerId: string) => void
  /** Replaces the built-in key input (onboarding owns its own field, T068-27). */
  apiKeyField?: React.ReactNode
  'data-testid'?: string
}

/**
 * The plan option standing for a company's variants that carry no `plan` at
 * all. A vendor with a base row and a coding-plan row offers a real choice
 * between them, and the base row needs a name to be choosable — the catalog
 * simply leaves its `plan` empty.
 */
export const BASE_PLAN_LABEL = 'Standard'

/** Human copy for a plan label; the catalog ships kebab-case codes. */
export function planLabel(plan: string): string {
  if (plan.length === 0) return BASE_PLAN_LABEL
  return plan
    .split('-')
    .map((part, index) => (index === 0 ? part.charAt(0).toUpperCase() + part.slice(1) : part))
    .join(' ')
}

export function ProviderDetailPanel({
  company,
  locale,
  onConfirm,
  onChange,
  onCancel,
  onSignIn,
  apiKeyField,
  'data-testid': testId = 'provider-detail-panel',
}: ProviderDetailPanelProps) {
  const effectiveLocale =
    locale !== undefined
      ? locale
      : typeof navigator !== 'undefined'
        ? navigator.language
        : null

  const inference = React.useMemo(
    () => inferRegionFromLocale(effectiveLocale, company.regions),
    [effectiveLocale, company.regions],
  )

  // The offered plans, with the empty "base row" plan first when the company
  // has variants that carry no plan at all.
  const planOptions = React.useMemo(() => {
    const hasBaseRow = company.variants.some((variant) => !variant.plan)
    return hasBaseRow ? ['', ...company.plans] : company.plans
  }, [company.variants, company.plans])

  const [plan, setPlan] = React.useState<string>(planOptions[0] ?? '')
  const [region, setRegion] = React.useState<string>(inference.region)
  const [authMethod, setAuthMethod] = React.useState<AuthMethod>('api_key')
  const [signInProviderId, setSignInProviderId] = React.useState<string>('')
  const [apiKey, setApiKey] = React.useState<string>('')

  const signInOptions: SignInOption[] = React.useMemo(
    () =>
      company.variants
        .filter((variant) => variant.auth_methods.includes('sign_in'))
        .map((variant) => ({ providerId: variant.id, label: variant.name })),
    [company.variants],
  )
  const apiKeyOffered = React.useMemo(
    () => company.variants.some((variant) => variant.auth_methods.includes('api_key')),
    [company.variants],
  )

  /**
   * The plan x region variant the two groups resolve to. A company with neither
   * dimension resolves to its primary; an unmatched combination falls back to
   * the primary rather than leaving the panel with nothing to save.
   */
  const resolvedVariant = React.useMemo(() => {
    const match = company.variants.find((variant) => {
      const planOk = planOptions.length === 0 || (variant.plan ?? '') === plan
      const regionOk = company.regions.length === 0 || (variant.region ?? '') === region
      return planOk && regionOk && variant.auth_methods.includes('api_key')
    })
    return match ?? company.primary
  }, [company, planOptions, plan, region])

  const selection: ProviderDetailSelection = {
    providerId: authMethod === 'sign_in' && signInProviderId ? signInProviderId : resolvedVariant.id,
    authMethod,
    plan: plan.length > 0 ? plan : undefined,
    region: company.regions.length > 0 ? region : undefined,
    apiKey: authMethod === 'api_key' ? apiKey : undefined,
  }

  // `onChange` reports the live draft; it must not fire during render.
  const selectionKey = `${selection.providerId}|${selection.authMethod}|${selection.plan ?? ''}|${selection.region ?? ''}|${selection.apiKey ?? ''}`
  const onChangeRef = React.useRef(onChange)
  onChangeRef.current = onChange
  const selectionRef = React.useRef(selection)
  selectionRef.current = selection
  React.useEffect(() => {
    onChangeRef.current?.(selectionRef.current)
  }, [selectionKey])

  const planGroupLabelId = `${testId}-plan-label`
  const regionGroupLabelId = `${testId}-region-label`

  return (
    <div
      data-testid={testId}
      aria-label={`Configure ${company.company}`}
      role="group"
      className="flex flex-col gap-3 rounded-md border p-3"
      style={{ borderColor: 'var(--color-border)' }}
    >
      <h3 className="text-sm" style={{ color: 'var(--color-secondary)' }}>
        {company.company}
      </h3>

      {/* ── Plan (FR-027) ──────────────────────────────────────────────── */}
      {planOptions.length > 1 && (
        <div className="flex flex-col gap-1">
          <span
            id={planGroupLabelId}
            className="text-xs uppercase"
            style={{ color: 'var(--color-muted)' }}
          >
            Plan
          </span>
          <div
            role="group"
            aria-labelledby={planGroupLabelId}
            data-testid={`${testId}-plans`}
            className="flex flex-wrap items-center gap-1"
          >
            {planOptions.map((value) => (
              <button
                key={value || 'standard'}
                type="button"
                tabIndex={0}
                data-testid={`${testId}-plan-${value || 'standard'}`}
                aria-pressed={plan === value}
                onClick={() => setPlan(value)}
                className="min-h-[32px] rounded border px-3 text-sm"
                style={{
                  borderColor: 'var(--color-border)',
                  color: 'var(--color-secondary)',
                  background: plan === value ? 'var(--color-surface-2)' : 'transparent',
                }}
              >
                {planLabel(value)}
              </button>
            ))}
          </div>
        </div>
      )}

      {/* ── Region, pre-selected from the locale (FR-027) ───────────────── */}
      {company.regions.length > 0 && (
        <div className="flex flex-col gap-1">
          <span
            id={regionGroupLabelId}
            data-testid={`${testId}-region-copy`}
            className="text-xs"
            style={{ color: 'var(--color-muted)' }}
          >
            {inference.copy}
          </span>
          <div
            role="group"
            aria-labelledby={regionGroupLabelId}
            data-testid={`${testId}-regions`}
            className="flex flex-wrap items-center gap-1"
          >
            {company.regions.map((value) => (
              <button
                key={value}
                type="button"
                tabIndex={0}
                data-testid={`${testId}-region-${value}`}
                aria-pressed={region === value}
                onClick={() => setRegion(value)}
                className="min-h-[32px] rounded border px-3 text-sm"
                style={{
                  borderColor: 'var(--color-border)',
                  color: 'var(--color-secondary)',
                  background: region === value ? 'var(--color-surface-2)' : 'transparent',
                }}
              >
                {regionLabel(value)}
              </button>
            ))}
          </div>
        </div>
      )}

      {/* ── Auth method, in the same step (FR-028) ──────────────────────── */}
      <AuthMethodControl
        data-testid={`${testId}-auth`}
        companyName={company.company}
        signInOptions={signInOptions}
        apiKeyOffered={apiKeyOffered}
        onMethodChange={setAuthMethod}
        onSignInProviderChange={setSignInProviderId}
        onSignIn={onSignIn}
        apiKeyField={
          apiKeyField ?? (
            <label className="flex flex-col gap-1 text-xs" htmlFor={`${testId}-api-key-input`}>
              API key
              <input
                id={`${testId}-api-key-input`}
                tabIndex={0}
                type="password"
                data-testid={`${testId}-api-key-input`}
                value={apiKey}
                onChange={(event) => setApiKey(event.target.value)}
                className="min-h-[32px] rounded border px-2 text-sm"
                style={{ borderColor: 'var(--color-border)' }}
              />
            </label>
          )
        }
      />

      <div className="flex items-center gap-2">
        <button
          type="button"
          tabIndex={0}
          data-testid={`${testId}-continue`}
          onClick={() => onConfirm?.(selection)}
          className="min-h-[32px] rounded border px-3 text-sm"
          style={{ borderColor: 'var(--color-border)', color: 'var(--color-secondary)' }}
        >
          Continue
        </button>
        {onCancel && (
          <button
            type="button"
            tabIndex={0}
            data-testid={`${testId}-cancel`}
            onClick={onCancel}
            className="min-h-[32px] rounded px-3 text-sm"
          >
            Cancel
          </button>
        )}
      </div>
    </div>
  )
}
