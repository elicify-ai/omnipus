import { useState, useMemo } from 'react'
import { createFileRoute, redirect, useNavigate, useRouteContext } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { AnimatePresence, motion } from 'framer-motion'
import {
  ArrowRight,
  ArrowLeft,
  Eye,
  EyeSlash,
  SpinnerGap,
  CheckCircle,
  XCircle,
  User,
  Key,
  Star,
  ChatCircle,
  CaretDown,
  Info,
  MagnifyingGlass,
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ModelSelector, type ModelGroup } from '@/components/ui/model-selector'
import { probeProvider, completeOnboardingTransaction, fetchAppState, isApiError } from '@/lib/api'
import { providersCatalogQueryOptions } from '@/lib/providersCatalogQuery'
import { pickCapableDefaultModel } from '@/lib/onboarding/defaultModel'
import OmnipusAvatar from '@/assets/logo/omnipus-avatar.svg?url'
import { PROVIDER_HINTS } from '@/lib/constants'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'
import { queryClient } from '@/lib/queryClient'
import { ProviderValidationBanner } from '@/components/providers/ProviderValidationBanner'
import type { ProviderValidation, CatalogProvider } from '@/lib/api/generated/openapi-types'
import { BrandIcon } from '@/components/ui/brand-icon'
import { BrandDisclaimer } from '@/components/ui/brand-disclaimer'
import { PLAN_LABELS, REGION_LABELS, DEFAULT_PLAN, planLabel, regionLabel } from '@/lib/providerLabels'
import { catalogEndpointHint, catalogLogoSlug, catalogSubtitle } from '@/lib/catalogDisplay'

// First-launch onboarding flow — full-screen, outside AppShell.
//
// Spec-6 FR-12.3: three numbered steps — name → password → model key — followed
// by an unnumbered "Meet your Assistant" completion screen that introduces Mia
// (the default ⭐ Assistant agent, auto-provisioned by coreagent.SeedConfig at
// gateway boot). The step indicator tracks the 3 numbered steps only; the
// completion screen is not a numbered step.

type Step = 1 | 2 | 3
type TestStatus = 'idle' | 'testing' | 'success' | 'error'

// ── Provider data model (ADR-068 FR-037) ──────────────────────────────────────
//
// The picker sources from the registry-fed catalog the gateway serves at
// GET /api/v1/providers/catalog (src/lib/api.ts::fetchProvidersCatalog) —
// there is NO bundled catalog (SC-010). Each CatalogProvider carries
//   id, name, company, api, plan?, region?, aliases, tier, auth_methods, models…
// and display strings (label, subtitle, logo slug, endpoint host) derive from
// src/lib/catalogDisplay.ts. A provider without an explicit `plan` is treated
// as DEFAULT_PLAN ('standard-api').
//
// Two-level grouped picker:
//   L1 — Company tiles (one per distinct company); multi-variant companies show ▾
//   L2 — Plan + Region segmented controls. Only the resolved subtitle/endpoint
//        hint is shown for context.

// Providers that REQUIRE a custom endpoint to function. The probe will always
// return "unknown provider" for these without an endpoint because no fixed
// default base exists (e.g. Azure is per-resource-host).
export const PROVIDERS_REQUIRING_ENDPOINT = new Set(['azure', 'azure-openai'])

// Plan/region labels live in src/lib/providerLabels.ts (shared with Settings'
// ProvidersSection, provider-ux-fixes-plan FIX-4) — re-exported here so
// existing `from './onboarding'` imports (incl. tests) keep working.
export { PLAN_LABELS, REGION_LABELS }

// Priority companies — surfaced first in the grid (Hick's law: reduce decision overload).
const PRIORITY_COMPANIES = ['OpenAI', 'Anthropic', 'OpenRouter']

// ── Catalog-based grouped-picker helpers ───────────────────────────────────────

/** The plan a catalog provider is filed under (DEFAULT_PLAN when unset). */
function planOf(entry: CatalogProvider): string {
  return entry.plan ?? DEFAULT_PLAN
}

/** Derive unique company names from the catalog (in declaration order, priority first). */
function uniqueCompanies(catalog: CatalogProvider[]): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const e of catalog) {
    if (!seen.has(e.company)) {
      seen.add(e.company)
      result.push(e.company)
    }
  }
  // Priority companies first (stable sort).
  const rank = (c: string) => {
    const i = PRIORITY_COMPANIES.indexOf(c)
    return i === -1 ? PRIORITY_COMPANIES.length : i
  }
  return result
    .map((c, i) => ({ c, i }))
    .sort((a, b) => rank(a.c) - rank(b.c) || a.i - b.i)
    .map(({ c }) => c)
}

/** All catalog entries for a given company. */
function entriesForCompany(catalog: CatalogProvider[], company: string): CatalogProvider[] {
  return catalog.filter((e) => e.company === company)
}

/** True when a company has more than one catalog entry (needs L2 plan/region UI). */
function isMultiVariant(catalog: CatalogProvider[], company: string): boolean {
  return entriesForCompany(catalog, company).length > 1
}

/** Plans offered by a company (in catalog order, unique). */
function plansForCompany(catalog: CatalogProvider[], company: string): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const e of entriesForCompany(catalog, company)) {
    const plan = planOf(e)
    if (!seen.has(plan)) {
      seen.add(plan)
      result.push(plan)
    }
  }
  return result
}

/** Regions offered by a company for a given plan (unique, in catalog order). */
function regionsForPlan(catalog: CatalogProvider[], company: string, plan: string): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const e of entriesForCompany(catalog, company)) {
    if (planOf(e) === plan && e.region !== undefined && !seen.has(e.region)) {
      seen.add(e.region)
      result.push(e.region)
    }
  }
  return result
}

/** Resolve the catalog entry for (company, plan, region) — unique per catalog. */
function resolveEntry(
  catalog: CatalogProvider[],
  company: string,
  plan: string,
  region: string | undefined,
): CatalogProvider | undefined {
  const companyEntries = entriesForCompany(catalog, company)
  const planCandidates = companyEntries.filter((e) => planOf(e) === plan)
  // If no regional split for this plan, return the (unique) match regardless of region.
  if (planCandidates.every((e) => e.region === undefined)) {
    return planCandidates[0]
  }
  const exactRegionMatch = planCandidates.find((e) => e.region === region)
  if (!exactRegionMatch && region !== undefined && import.meta.env.DEV) {
    // Falling back to planCandidates[0] silently substitutes a DIFFERENT
    // region's entry rather than returning undefined (which would collapse
    // the API-key panel — see handleSelectRegion's own fallback). Not
    // triggerable today: every (company, plan, region) combination the L1/L2
    // UI can produce has a matching catalog entry. Warn loudly in dev so a
    // future catalog edit that breaks this invariant is caught immediately
    // instead of silently resolving to the wrong provider id.
    console.warn(
      `[onboarding] resolveEntry: no exact region match for ${company}/${plan}/${region} — falling back to ${planCandidates[0]?.id ?? '(none)'}`,
    )
  }
  return exactRegionMatch ?? planCandidates[0]
}

/** The logo slug for a company (taken from the first entry). */
function logoSlugForCompany(catalog: CatalogProvider[], company: string): string {
  const first = entriesForCompany(catalog, company)[0]
  return first ? catalogLogoSlug(first) : ''
}

/** Filter company tiles by a search term (company name + entry aliases, case-insensitive). */
function filterCompanies(catalog: CatalogProvider[], query: string): string[] {
  const q = query.trim().toLowerCase()
  const companies = uniqueCompanies(catalog)
  if (!q) return companies
  return companies.filter((company) => {
    if (company.toLowerCase().includes(q)) return true
    const aliases = catalog
      .filter((e) => e.company === company)
      .flatMap((e) => [e.name, ...e.aliases])
    return aliases.some((a) => a.toLowerCase().includes(q))
  })
}

// Lightweight, dependency-free password strength heuristic. Scores on length
// plus character-class diversity (lower / upper / digit / symbol). Returns a
// 1–4 score with a human label and a brand token for the meter fill (or null
// for empty input, so the meter is hidden until the user types).
type PasswordStrengthLabel = 'Too short' | 'Weak' | 'Fair' | 'Good' | 'Strong'
type PasswordStrengthColor =
  | 'var(--color-error)'
  | 'var(--color-warning)'
  | 'var(--color-accent)'
  | 'var(--color-success)'
type PasswordStrength = {
  score: 1 | 2 | 3 | 4
  label: PasswordStrengthLabel
  color: PasswordStrengthColor
}

// Exported for unit testing (table-driven coverage of the score boundaries).
export function evaluatePasswordStrength(pw: string): PasswordStrength | null {
  if (!pw) return null
  // Passwords below the 8-char minimum are always "Too short" (score 1),
  // regardless of character diversity, to match the validation gate. This
  // early return also guarantees the score below is always 1–4 (never 0).
  if (pw.length < 8) {
    return { score: 1, label: 'Too short', color: 'var(--color-error)' }
  }
  let points = 1 // length >= 8 (guaranteed by the early return above)
  if (pw.length >= 12) points += 1
  const classes =
    (/[a-z]/.test(pw) ? 1 : 0) +
    (/[A-Z]/.test(pw) ? 1 : 0) +
    (/[0-9]/.test(pw) ? 1 : 0) +
    (/[^A-Za-z0-9]/.test(pw) ? 1 : 0)
  if (classes >= 2) points += 1
  if (classes >= 3) points += 1
  const score = Math.min(points, 4) as PasswordStrength['score']
  switch (score) {
    case 1:
      return { score, label: 'Weak', color: 'var(--color-error)' }
    case 2:
      return { score, label: 'Fair', color: 'var(--color-warning)' }
    case 3:
      return { score, label: 'Good', color: 'var(--color-accent)' }
    case 4:
      return { score, label: 'Strong', color: 'var(--color-success)' }
  }
}

// Maps a raw upstream probe error string (e.g. "upstream models: status 401")
// to a plain-language, actionable message for the given provider. The raw
// string is preserved separately by the caller behind a "Technical details"
// disclosure so debugging info is never lost. Exported for unit testing.
//
// Branch order (first match wins):
//   1. "needs endpoint" — provider unknown / requires a custom base URL
//   2. Auth rejected (401 / 403 / invalid-key text)
//   3. Rate limited (429)
//   4. Upstream 400 / 404 — bad request or wrong endpoint/region
//   5. Upstream 5xx — provider server error
//   6. Fallback — real network / timeout / unknown
export function friendlyProbeError(raw: string, providerName: string): string {
  const r = (raw || '').toLowerCase()
  const has = (...codes: string[]) =>
    codes.some((c) => new RegExp(`(^|[^0-9])${c}([^0-9]|$)`).test(r))

  // Branch 1 — provider wiring gap or explicit "needs endpoint" response.
  // Matches the 400 "unknown provider" or "requires an endpoint" messages
  // returned when the backend has no base URL for the selected id.
  if (/unknown provider|requires?\s+(an?\s+)?endpoint|no endpoint/.test(r)) {
    return `${providerName} needs a custom API endpoint. Enter it below and retry.`
  }

  // Branch 2 — key rejected.
  if (has('401', '403') || /unauthor|forbidden|invalid api key|invalid key|rejected/.test(r)) {
    return `That API key was rejected by ${providerName}. Double-check you copied the full key and that it's active, then retry.`
  }

  // Branch 3 — rate limited.
  if (has('429') || /rate.?limit|too many requests/.test(r)) {
    return `Rate limited by ${providerName}. Wait a moment and retry.`
  }

  // Branch 4 — upstream 400 / 404 (bad request or wrong endpoint/region).
  if (has('400', '404') || /not found|bad request/.test(r)) {
    return `${providerName} rejected the request — check the endpoint and that your key is for this region/platform.`
  }

  // Branch 5 — upstream 5xx (provider server error).
  if (/status 5\d\d/.test(r)) {
    return `${providerName} is having server issues. Try again shortly.`
  }

  // Branch 6 — fallback: real network/timeout/unknown.
  return `Couldn't reach ${providerName}. Check your connection and the key, then retry.`
}

// Eye show/hide toggle button: pads the hit area to a 44x44 mobile tap target
// (touch min) without enlarging the 14px icon — the icon is centered in the
// padded box. Collapses to a snug box on sm+ (pointer). Shared by every
// password/key field in onboarding + login.
const EYE_TOGGLE_CLASS =
  'absolute right-1 sm:right-2.5 top-1/2 -translate-y-1/2 inline-flex items-center justify-center min-h-11 min-w-11 sm:min-h-0 sm:min-w-0 transition-colors'

const stepVariants = {
  enter: (direction: number) => ({
    x: direction > 0 ? 36 : -36,
    opacity: 0,
  }),
  center: { x: 0, opacity: 1 },
  exit: (direction: number) => ({
    x: direction > 0 ? -36 : 36,
    opacity: 0,
  }),
}

function OnboardingWizard() {
  const navigate = useNavigate()
  const { addToast } = useUiStore()
  const { appStateBannerMessage } = useRouteContext({ from: '/onboarding' })

  // Source providers from the registry-fed catalog (ADR-068 FR-037), on the
  // shared ETag re-validation policy (providersCatalogQuery.ts).
  const {
    data: catalogDoc,
    isError: catalogError,
    isLoading: catalogLoading,
    refetch: refetchCatalog,
  } = useQuery(providersCatalogQueryOptions())
  const providers = useMemo(() => catalogDoc?.providers ?? [], [catalogDoc])

  const [step, setStep] = useState<Step>(1)
  const [direction, setDirection] = useState(1)
  // `completed` flips true once completeOnboardingTransaction succeeds; the
  // numbered step indicator is hidden and the unnumbered "Meet your Assistant"
  // completion screen is rendered instead.
  const [completed, setCompleted] = useState(false)
  // selectedProvider is the resolved backend id (the leaf of the L1→L2 selection).
  // selectedCompany is the L1 company tile; selectedPlan/Region are the L2 controls.
  const [selectedProvider, setSelectedProvider] = useState('')
  const [selectedCompany, setSelectedCompany] = useState('')
  const [selectedPlan, setSelectedPlan] = useState<string>(DEFAULT_PLAN)
  const [selectedRegion, setSelectedRegion] = useState<string | undefined>('intl')
  const [apiKey, setApiKey] = useState('')
  const [endpoint, setEndpoint] = useState('')
  const [showKey, setShowKey] = useState(false)
  const [testStatus, setTestStatus] = useState<TestStatus>('idle')
  const [testError, setTestError] = useState('')
  const [selectedModel, setSelectedModel] = useState('')
  const [availableModels, setAvailableModels] = useState<string[]>([])
  const [isSaving, setIsSaving] = useState(false)
  // Surfaced inline on the model-key step when completeOnboardingTransaction
  // fails, so the user stays on the step and can retry rather than failing
  // silently.
  const [finishError, setFinishError] = useState('')
  // Non-blocking validation warning from the last probe (no_credit / unreachable
  // / restricted). Cleared when the user changes provider or re-probes.
  const [probeValidation, setProbeValidation] = useState<ProviderValidation | undefined>(undefined)
  // Step 1 — name/username
  const [adminUsername, setAdminUsername] = useState('')
  // Step 2 — password + confirm
  const [adminPassword, setAdminPassword] = useState('')
  const [adminPasswordConfirm, setAdminPasswordConfirm] = useState('')
  const [showAdminPassword, setShowAdminPassword] = useState(false)
  const [adminError, setAdminError] = useState('')

  const providerHintText = selectedProvider ? PROVIDER_HINTS[selectedProvider] : undefined

  const goTo = (next: Step) => {
    setDirection(next > step ? 1 : -1)
    setStep(next)
  }

  // Reset connection state (called when company, plan, or region changes).
  const resetConnection = () => {
    setApiKey('')
    setEndpoint('')
    setTestStatus('idle')
    setTestError('')
    setSelectedModel('')
    setAvailableModels([])
    setProbeValidation(undefined)
  }

  // L1: User clicks a company tile. For single-option companies, this immediately
  // sets the provider id. For multi-variant companies, the L2 panel expands and
  // the plan/region defaults resolve the first id.
  const handleSelectCompany = (company: string) => {
    if (selectedCompany === company) return // already selected, no-op
    setSelectedCompany(company)

    // Default plan to DEFAULT_PLAN; default region to 'intl' (spec defaults).
    const newPlan = DEFAULT_PLAN
    const newRegion = 'intl'
    setSelectedPlan(newPlan)
    setSelectedRegion(newRegion)

    // Resolve the backend id from the defaults.
    const entry = resolveEntry(providers, company, newPlan, newRegion)
    const resolvedId = entry?.id ?? entriesForCompany(providers, company)[0]?.id ?? ''
    setSelectedProvider(resolvedId)
    resetConnection()
  }

  // L2: User changes the plan. Re-resolve the id and reset the model list
  // (stale models from a different endpoint are wrong picks — spec requirement).
  const handleSelectPlan = (plan: string) => {
    setSelectedPlan(plan)
    // Check if this plan has regions.
    const regions = regionsForPlan(providers, selectedCompany, plan)
    let newRegion = selectedRegion
    if (regions.length > 0 && (selectedRegion === undefined || !regions.includes(selectedRegion))) {
      // Current region not valid for this plan — default to intl or first available.
      newRegion = regions.includes('intl') ? 'intl' : regions[0]
      setSelectedRegion(newRegion)
    }
    const entry = resolveEntry(providers, selectedCompany, plan, newRegion)
    const resolvedId = entry?.id ?? entriesForCompany(providers, selectedCompany).find((e) => planOf(e) === plan)?.id ?? ''
    setSelectedProvider(resolvedId)
    // Reset model list — changing plan means a different endpoint and different models.
    setAvailableModels([])
    setSelectedModel('')
    if (testStatus !== 'idle') {
      setTestStatus('idle')
      setTestError('')
    }
  }

  // L2: User changes the region. Re-resolve the id and reset the model list.
  const handleSelectRegion = (region: string | undefined) => {
    setSelectedRegion(region)
    const entry = resolveEntry(providers, selectedCompany, selectedPlan, region)
    // Fall back to any entry for this company+plan (mirrors handleSelectPlan) so
    // a region that doesn't resolve can never silently empty selectedProvider
    // and collapse the API-key panel.
    const resolvedId =
      entry?.id ??
      entriesForCompany(providers, selectedCompany).find((e) => planOf(e) === selectedPlan)?.id ??
      ''
    setSelectedProvider(resolvedId)
    // Reset model list — different region = different endpoint + different models.
    setAvailableModels([])
    setSelectedModel('')
    if (testStatus !== 'idle') {
      setTestStatus('idle')
      setTestError('')
    }
  }

  // Legacy: direct provider id selection (kept for backward compat with tests
  // that click a single-option tile and expect a provider id set).
  const handleSelectProvider = (id: string) => {
    setSelectedProvider(id)
    setApiKey('')
    setEndpoint('')
    setTestStatus('idle')
    setTestError('')
    setSelectedModel('')
    setAvailableModels([])
  }

  const handleApiKeyChange = (k: string) => {
    setApiKey(k)
    if (testStatus !== 'idle') {
      setTestStatus('idle')
      setTestError('')
    }
  }

  const handleEndpointChange = (v: string) => {
    setEndpoint(v)
    if (testStatus !== 'idle') {
      setTestStatus('idle')
      setTestError('')
    }
  }

  const handleTest = async () => {
    if (!selectedProvider || !apiKey.trim()) return
    // For providers requiring an endpoint (e.g. Azure), block the probe until
    // the endpoint field has a value. The Connect button is also disabled in
    // the UI, but this guard prevents any programmatic bypass.
    if (PROVIDERS_REQUIRING_ENDPOINT.has(selectedProvider) && !endpoint.trim()) return
    setTestStatus('testing')
    setTestError('')
    setProbeValidation(undefined)
    try {
      // Non-persistent test + fetch: the server probes the provider with the
      // supplied key and returns the model list in one response. Nothing is
      // saved to disk until the user clicks "Complete setup" on step 3, which
      // fires /onboarding/complete with the full payload atomically.
      const endpointArg = endpoint.trim() || undefined
      const result = await probeProvider(selectedProvider, apiKey.trim(), endpointArg)
      if (result.success) {
        setTestStatus('success')
        // Capture any non-blocking validation warning from the probe result
        // (no_credit / unreachable / restricted). success=true means proceed.
        if (result.validation && result.validation.outcome !== 'valid') {
          setProbeValidation(result.validation)
        }
        if (result.models && result.models.length > 0) {
          setAvailableModels(result.models)
          // UAT fix: pre-select a capable default instead of leaving the
          // field empty or letting the first (often a tiny/preview/404)
          // entry win. Only seed when the user has not already picked one
          // this session (so a re-test doesn't clobber a deliberate choice).
          setSelectedModel((prev) =>
            prev.trim() !== '' ? prev : pickCapableDefaultModel(result.models ?? []),
          )
        }
      } else {
        setTestStatus('error')
        setTestError(result.error ?? 'Connection test failed')
      }
    } catch (err) {
      setTestStatus('error')
      setTestError(err instanceof Error ? err.message : String(err))
    }
  }

  // Model selection is kept purely in local state during onboarding. It gets
  // persisted to the server as part of /onboarding/complete's payload, so we
  // intentionally do NOT fire a PUT /providers/{id} here — that would require
  // a __Host-csrf cookie the browser cannot install over plain HTTP.

  // Step 1 → 2: validate the username before advancing.
  const handleNameContinue = () => {
    if (!adminUsername.trim()) {
      setAdminError('Choose a username to continue')
      return
    }
    setAdminError('')
    goTo(2)
  }

  // Step 2 → 3: validate the password before advancing.
  const handlePasswordContinue = () => {
    if (adminPassword.length < 8) {
      setAdminError('Password must be at least 8 characters')
      return
    }
    if (adminPassword !== adminPasswordConfirm) {
      setAdminError('Passwords do not match')
      return
    }
    setAdminError('')
    goTo(3)
  }

  // Step 3 → completion: fire the atomic onboarding transaction. On success,
  // the gateway has already issued the omnipus-session HttpOnly cookie
  // (US-5 / FR-011) — the SPA only needs to remember the display-only
  // username, then reveal the "Meet your Assistant" screen. On failure,
  // surface the error inline on step 3 so the user can retry without losing
  // their place.
  const handleComplete = async () => {
    setIsSaving(true)
    setFinishError('')
    try {
      const resp = await completeOnboardingTransaction({
        provider: {
          // ADR-068 T068-06: provider is a discriminated union on auth_method.
          // This screen only collects API keys today; the sign_in variant is
          // wired by T068-16.
          auth_method: 'api_key',
          id: selectedProvider,
          api_key: apiKey,
          model: selectedModel,
          // Persist a custom endpoint (required for azure; optional regional
          // override for others) so the saved provider config can reach it.
          ...(endpoint.trim() ? { endpoint: endpoint.trim() } : {}),
        },
        admin: {
          username: adminUsername,
          password: adminPassword,
        },
      })
      useAuthStore.getState().setUsername(resp.username)
      // Bugfix (slash-palette silent-empty): completeOnboardingTransaction is
      // the FIRST point the omnipus-session cookie exists on a fresh
      // install. The commands query (['commands','web'],
      // src/hooks/useSlashMenu.ts + ChatScreen.tsx) is behind withAuth and
      // may have already 401'd — and gone permanently errored — from a
      // composer that mounted before this cookie was set. Invalidate now so
      // every mounted observer of the shared ['commands', ...] cache entry
      // refetches once the user reaches chat, instead of staying empty for
      // the rest of the page session (only a manual reload used to recover).
      queryClient.invalidateQueries({ queryKey: ['commands'] })
      // Same reasoning, same moment, different cache entry: ['workspaces'] is
      // read by DefaultWorkspaceRedirect to decide where the user lands, and
      // it is shared with Sidebar's 30s poll. On a fresh install any observer
      // that mounted before this cookie existed holds an errored or empty
      // entry, and the landing decision is made from it. The ['commands']
      // invalidation above was added for exactly this failure and was never
      // generalised to the key that governs where the user actually ends up.
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      setCompleted(true)
    } catch (err) {
      // Surface the failure both inline (so the user stays on step 3 and can
      // retry) and as a toast — never strand the error silently.
      const message = `Could not complete setup: ${err instanceof Error ? err.message : 'Unknown error'}`
      setFinishError(message)
      addToast({ message, variant: 'error' })
    } finally {
      setIsSaving(false)
    }
  }

  const handleStartChatting = () => {
    navigate({ to: '/' })
  }

  return (
    <div
      className="h-screen flex flex-col items-center p-6 relative overflow-y-auto overflow-x-hidden overscroll-y-contain"
      style={{ backgroundColor: 'var(--color-primary)', color: 'var(--color-secondary)', justifyContent: 'safe center' }}
    >
      {/* Atmospheric depth — subtle Forge Gold radial glow */}
      <div
        aria-hidden
        className="fixed inset-0 pointer-events-none"
        style={{
          background:
            'radial-gradient(ellipse 65% 55% at 50% 50%, rgba(212,175,55,0.055) 0%, transparent 68%)',
        }}
      />
      {/* Top edge accent line */}
      <div
        aria-hidden
        className="fixed top-0 left-0 right-0 h-px pointer-events-none"
        style={{
          background:
            'linear-gradient(90deg, transparent 0%, rgba(212,175,55,0.35) 50%, transparent 100%)',
        }}
      />

      {/* Server error banner — surfaces when /api/v1/state returned 5xx during beforeLoad */}
      {appStateBannerMessage && (
        <div
          role="alert"
          className="fixed top-4 left-1/2 -translate-x-1/2 w-full max-w-md z-20 rounded-md border border-red-500/40 bg-red-500/10 px-4 py-2.5 text-sm text-red-400"
        >
          {appStateBannerMessage}
        </div>
      )}

      {/* Safe-centering wrapper: my-auto centers the step block vertically when
          it fits the viewport, but collapses to 0 and lets the page scroll when
          content overflows (e.g. Step 3 with a multi-variant provider like ZAI
          expands the plan/region panel + API-key field and would otherwise push
          the Back / Complete Setup buttons below the fold with no way to scroll
          — overflow-hidden + justify-center clipped both ends). */}
      <div className="w-full flex flex-col items-center">
      {/* Step indicator — labeled for assistive tech so screen readers announce
          progress. The dots themselves are decorative (aria-hidden); the
          progressbar role + valuenow/min/max + aria-label carry the semantics,
          and the sr-only line gives a plain-text "Step X of N" announcement.
          Hidden on the unnumbered "Meet your Assistant" completion screen. */}
      {!completed && (
        <div className="flex flex-col items-center gap-2 mb-12 z-10">
          {/* Visible step counter for sighted users — the dots alone are unlabeled. */}
          <span
            aria-hidden
            className="text-xs font-medium tracking-wide"
            style={{ color: 'var(--color-muted)' }}
          >
            Step {step} of 3
          </span>
          <div
            className="flex items-center gap-2"
            role="progressbar"
            aria-valuenow={step}
            aria-valuemin={1}
            aria-valuemax={3}
            aria-label={`Onboarding progress: step ${step} of 3`}
          >
            <span className="sr-only">Step {step} of 3</span>
            {([1, 2, 3] as Step[]).map((s) => (
              <motion.div
                key={s}
                aria-hidden
                animate={{
                  width: s === step ? 24 : 8,
                  backgroundColor:
                    s === step
                      ? '#d4af37'
                      : s < step
                      ? 'rgba(212,175,55,0.45)'
                      : '#2d3748',
                }}
                transition={{ duration: 0.3, ease: 'easeInOut' }}
                className="h-2 rounded-full"
              />
            ))}
          </div>
        </div>
      )}

      {/* Animated step content */}
      <div className="w-full max-w-md z-10">
        <AnimatePresence mode="wait" custom={direction}>
          {completed ? (
            <motion.div
              key="meet-assistant"
              custom={direction}
              variants={stepVariants}
              initial="enter"
              animate="center"
              exit="exit"
              transition={{ duration: 0.22, ease: 'easeInOut' }}
            >
              <MeetAssistantStep onStartChatting={handleStartChatting} />
            </motion.div>
          ) : step === 1 ? (
            <motion.div
              key="step1"
              custom={direction}
              variants={stepVariants}
              initial="enter"
              animate="center"
              exit="exit"
              transition={{ duration: 0.22, ease: 'easeInOut' }}
            >
              <NameStep
                username={adminUsername}
                onUsernameChange={setAdminUsername}
                error={adminError}
                onContinue={handleNameContinue}
              />
            </motion.div>
          ) : step === 2 ? (
            <motion.div
              key="step2"
              custom={direction}
              variants={stepVariants}
              initial="enter"
              animate="center"
              exit="exit"
              transition={{ duration: 0.22, ease: 'easeInOut' }}
            >
              <PasswordStep
                password={adminPassword}
                onPasswordChange={setAdminPassword}
                passwordConfirm={adminPasswordConfirm}
                onPasswordConfirmChange={setAdminPasswordConfirm}
                showPassword={showAdminPassword}
                onToggleShowPassword={() => setShowAdminPassword((v) => !v)}
                error={adminError}
                onContinue={handlePasswordContinue}
                onBack={() => goTo(1)}
              />
            </motion.div>
          ) : (
            <motion.div
              key="step3"
              custom={direction}
              variants={stepVariants}
              initial="enter"
              animate="center"
              exit="exit"
              transition={{ duration: 0.22, ease: 'easeInOut' }}
            >
              <ModelKeyStep
                providers={providers}
                catalogLoading={catalogLoading}
                catalogError={catalogError}
                onRetryCatalog={() => { void refetchCatalog() }}
                selectedProvider={selectedProvider}
                selectedCompany={selectedCompany}
                selectedPlan={selectedPlan}
                selectedRegion={selectedRegion}
                onSelectCompany={handleSelectCompany}
                onSelectPlan={handleSelectPlan}
                onSelectRegion={handleSelectRegion}
                onSelect={handleSelectProvider}
                apiKey={apiKey}
                onApiKeyChange={handleApiKeyChange}
                endpoint={endpoint}
                onEndpointChange={handleEndpointChange}
                showKey={showKey}
                onToggleShowKey={() => setShowKey((v) => !v)}
                testStatus={testStatus}
                testError={testError}
                onTest={handleTest}
                onBack={() => goTo(2)}
                onComplete={handleComplete}
                providerHint={providerHintText}
                availableModels={availableModels}
                selectedModel={selectedModel}
                onSelectModel={setSelectedModel}
                isSaving={isSaving}
                finishError={finishError}
                probeValidation={probeValidation}
              />
            </motion.div>
          )}
        </AnimatePresence>
      </div>
      </div>
    </div>
  )
}

// ── Step 1: What should I call you? ────────────────────────────────────────────

function NameStep({
  username,
  onUsernameChange,
  error,
  onContinue,
}: {
  username: string
  onUsernameChange: (v: string) => void
  error: string
  onContinue: () => void
}) {
  return (
    <div className="flex flex-col items-center text-center gap-6">
      <motion.div
        initial={{ scale: 0.8, opacity: 0 }}
        animate={{ scale: 1, opacity: 1 }}
        transition={{ duration: 0.4 }}
      >
        <div
          className="h-16 w-16 rounded-full flex items-center justify-center"
          style={{ backgroundColor: 'rgba(212,175,55,0.12)' }}
        >
          <User size={28} weight="duotone" style={{ color: 'var(--color-accent)' }} />
        </div>
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.15, duration: 0.38 }}
      >
        <h2 className="font-headline text-3xl font-bold mb-2"
          style={{ color: 'var(--color-secondary)' }}>
          What should I call you?
        </h2>
        <p className="text-sm" style={{ color: 'var(--color-muted)' }}>
          Choose a username — this is the one account for your Omnipus
        </p>
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.25, duration: 0.38 }}
        className="w-full space-y-4"
      >
        <div>
          <label htmlFor="admin-username" className="text-xs font-medium mb-1.5 block"
            style={{ color: 'var(--color-muted)' }}>
            Username
          </label>
          <Input
            id="admin-username"
            type="text"
            value={username}
            onChange={(e) => onUsernameChange(e.target.value)}
            placeholder="admin"
            autoComplete="username"
            autoFocus
            onKeyDown={(e) => {
              if (e.key === 'Enter') onContinue()
            }}
          />
        </div>

        {/* Error feedback */}
        {error && (
          <div
            data-testid="onboarding-error"
            role="alert"
            aria-live="assertive"
            className="flex items-start gap-2 text-sm"
            style={{ color: 'var(--color-error)' }}
          >
            <XCircle size={14} weight="fill" className="shrink-0 mt-0.5" />
            <span>{error}</span>
          </div>
        )}
      </motion.div>

      {/* Navigation */}
      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.35, duration: 0.38 }}
        className="w-full"
      >
        <Button
          className="w-full h-11 gap-2 font-headline font-bold text-base"
          onClick={onContinue}
          disabled={!username.trim()}
        >
          Continue
          <ArrowRight size={16} weight="bold" />
        </Button>
      </motion.div>
    </div>
  )
}

// ── Step 2: Set your password ──────────────────────────────────────────────────

function PasswordStep({
  password,
  onPasswordChange,
  passwordConfirm,
  onPasswordConfirmChange,
  showPassword,
  onToggleShowPassword,
  error,
  onContinue,
  onBack,
}: {
  password: string
  onPasswordChange: (v: string) => void
  passwordConfirm: string
  onPasswordConfirmChange: (v: string) => void
  showPassword: boolean
  onToggleShowPassword: () => void
  error: string
  onContinue: () => void
  onBack: () => void
}) {
  const isValid = password.length >= 8 && password === passwordConfirm
  const strength = evaluatePasswordStrength(password)
  return (
    <div className="flex flex-col items-center text-center gap-6">
      <motion.div
        initial={{ scale: 0.8, opacity: 0 }}
        animate={{ scale: 1, opacity: 1 }}
        transition={{ duration: 0.4 }}
      >
        <div
          className="h-16 w-16 rounded-full flex items-center justify-center"
          style={{ backgroundColor: 'rgba(212,175,55,0.12)' }}
        >
          <Key size={28} weight="duotone" style={{ color: 'var(--color-accent)' }} />
        </div>
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.15, duration: 0.38 }}
      >
        <h2 className="font-headline text-3xl font-bold mb-2"
          style={{ color: 'var(--color-secondary)' }}>
          Set your password
        </h2>
        <p className="text-sm" style={{ color: 'var(--color-muted)' }}>
          This unlocks your Omnipus — store it somewhere safe
        </p>
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.25, duration: 0.38 }}
        className="w-full space-y-4"
      >
        {/* Password */}
        <div>
          <label htmlFor="admin-password" className="text-xs font-medium mb-1.5 block"
            style={{ color: 'var(--color-muted)' }}>
            Password
          </label>
          <div className="relative">
            <Input
              id="admin-password"
              type={showPassword ? 'text' : 'password'}
              value={password}
              onChange={(e) => onPasswordChange(e.target.value)}
              placeholder="Min. 8 characters"
              autoComplete="new-password"
              className="pr-9"
              autoFocus
            />
            <button tabIndex={0}
              type="button"
              onClick={onToggleShowPassword}
              className={EYE_TOGGLE_CLASS}
              style={{ color: 'var(--color-muted)' }}
              aria-label={showPassword ? 'Hide password' : 'Show password'}
            >
              {showPassword ? <EyeSlash size={14} /> : <Eye size={14} />}
            </button>
          </div>
          {/* Inline password-strength meter — length + character-class heuristic. */}
          {strength && (
            <div className="mt-2" data-testid="password-strength">
              <div
                className="flex gap-1"
                role="meter"
                aria-label="Password strength"
                aria-valuenow={strength.score}
                aria-valuemin={0}
                aria-valuemax={4}
                aria-valuetext={strength.label}
              >
                {[1, 2, 3, 4].map((seg) => (
                  <div
                    key={seg}
                    className="h-1 flex-1 rounded-full transition-colors duration-200"
                    style={{
                      backgroundColor:
                        seg <= strength.score ? strength.color : 'var(--color-surface-2)',
                    }}
                  />
                ))}
              </div>
              <p className="text-xs mt-1 font-medium" style={{ color: strength.color }}>
                {strength.label}
              </p>
            </div>
          )}
        </div>

        {/* Confirm Password */}
        <div>
          <label htmlFor="admin-password-confirm" className="text-xs font-medium mb-1.5 block"
            style={{ color: 'var(--color-muted)' }}>
            Confirm Password
          </label>
          <div className="relative">
            <Input
              id="admin-password-confirm"
              type={showPassword ? 'text' : 'password'}
              value={passwordConfirm}
              onChange={(e) => onPasswordConfirmChange(e.target.value)}
              placeholder="Repeat password"
              autoComplete="new-password"
              className="pr-9"
            />
            <button tabIndex={0}
              type="button"
              onClick={onToggleShowPassword}
              className={EYE_TOGGLE_CLASS}
              style={{ color: 'var(--color-muted)' }}
              aria-label={showPassword ? 'Hide password' : 'Show password'}
            >
              {showPassword ? <EyeSlash size={14} /> : <Eye size={14} />}
            </button>
          </div>
        </div>

        {/* Error feedback */}
        {error && (
          <div
            data-testid="onboarding-error"
            role="alert"
            aria-live="assertive"
            className="flex items-start gap-2 text-sm"
            style={{ color: 'var(--color-error)' }}
          >
            <XCircle size={14} weight="fill" className="shrink-0 mt-0.5" />
            <span>{error}</span>
          </div>
        )}
      </motion.div>

      {/* Navigation */}
      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.35, duration: 0.38 }}
        className="flex items-center gap-3 pt-2 w-full"
      >
        <Button variant="ghost" className="gap-1.5 min-h-11 sm:min-h-0" onClick={onBack}>
          <ArrowLeft size={14} />
          Back
        </Button>
        <Button
          className="flex-1 gap-2 font-headline font-bold"
          onClick={onContinue}
          disabled={!isValid}
        >
          Continue
          <ArrowRight size={14} weight="bold" />
        </Button>
      </motion.div>
    </div>
  )
}

// ── Step 3: Add a model key ────────────────────────────────────────────────────

function ModelKeyStep({
  providers,
  catalogLoading,
  catalogError,
  onRetryCatalog,
  selectedProvider,
  selectedCompany,
  selectedPlan,
  selectedRegion,
  onSelectCompany,
  onSelectPlan,
  onSelectRegion,
  apiKey,
  onApiKeyChange,
  endpoint,
  onEndpointChange,
  showKey,
  onToggleShowKey,
  testStatus,
  testError,
  onTest,
  onBack,
  onComplete,
  providerHint,
  availableModels,
  selectedModel,
  onSelectModel,
  isSaving,
  finishError,
  probeValidation,
}: {
  providers: CatalogProvider[]
  catalogLoading: boolean
  catalogError: boolean
  onRetryCatalog: () => void
  selectedProvider: string
  selectedCompany: string
  selectedPlan: string
  selectedRegion: string | undefined
  onSelectCompany: (company: string) => void
  onSelectPlan: (plan: string) => void
  onSelectRegion: (region: string | undefined) => void
  /** Kept for backward-compat paths (Azure direct click, etc.) */
  onSelect: (id: string) => void
  apiKey: string
  onApiKeyChange: (k: string) => void
  endpoint: string
  onEndpointChange: (v: string) => void
  showKey: boolean
  onToggleShowKey: () => void
  testStatus: TestStatus
  testError: string
  onTest: () => void
  onBack: () => void
  onComplete: () => void
  providerHint?: string
  availableModels: string[]
  selectedModel: string
  onSelectModel: (model: string) => void
  isSaving: boolean
  finishError: string
  probeValidation?: ProviderValidation
}) {
  const [searchQuery, setSearchQuery] = useState('')
  // Provider-picker accordion: once a company is selected the tall search+grid
  // collapses into a one-line summary (the grid is the single tallest element on
  // this step), keeping the form short enough to fit the viewport without
  // scrolling — critical on touch/iPad, where a scrollable form can rubber-band
  // the submit button back below the fold. "Change" re-expands the grid.
  const [pickerOpen, setPickerOpen] = useState(() => !selectedCompany)

  // Derive filtered company list (search + stable priority order for the grid).
  // filterCompanies already returns companies with priority companies first.
  const filteredCompanies = useMemo(
    () => filterCompanies(providers, searchQuery),
    [providers, searchQuery],
  )

  // Derive L2 options for the selected company.
  const companyPlans = selectedCompany ? plansForCompany(providers, selectedCompany) : []
  const currentPlanRegions = selectedCompany
    ? regionsForPlan(providers, selectedCompany, selectedPlan)
    : []
  const hasRegionForPlan = currentPlanRegions.length > 0
  const multiVariant = selectedCompany ? isMultiVariant(providers, selectedCompany) : false

  // The resolved catalog entry (for endpointHint + wire badge display).
  const resolvedEntry = selectedCompany
    ? resolveEntry(providers, selectedCompany, selectedPlan, selectedRegion) ??
      entriesForCompany(providers, selectedCompany)[0]
    : undefined

  // Build providerGroups for the ModelSelector.
  const providerGroups: ModelGroup[] =
    availableModels.length > 0 && selectedCompany
      ? [{ providerName: selectedCompany, models: availableModels }]
      : []

  const continueEnabled = testStatus === 'success' && !!selectedModel.trim()
  const requiresEndpoint = PROVIDERS_REQUIRING_ENDPOINT.has(selectedProvider)
  const connectDisabled =
    !apiKey.trim() ||
    testStatus === 'testing' ||
    (requiresEndpoint && !endpoint.trim())

  // Friendly name for the error message — use company name if available.
  const providerDisplayName = selectedCompany || 'the provider'

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h2
          className="font-headline text-2xl font-bold mb-1"
          style={{ color: 'var(--color-secondary)' }}
        >
          Add a model key
        </h2>
        <p className="text-sm" style={{ color: 'var(--color-muted)' }}>
          Omnipus needs an AI provider to power your agents.
        </p>
        <p className="text-xs mt-1" style={{ color: 'var(--color-muted)' }}>
          Not sure? OpenAI or OpenRouter are good starting points.
        </p>
      </div>

      {/* Selected-provider summary — collapses the search + company grid below
          it once a company is picked, so this step stays short. */}
      {selectedCompany && !pickerOpen && (
        <div
          className="rounded-lg border p-3 flex items-center justify-between gap-2"
          style={{ borderColor: 'var(--color-accent)', backgroundColor: 'rgba(212,175,55,0.06)' }}
        >
          <div className="flex items-center gap-2 min-w-0">
            <BrandIcon
              slug={logoSlugForCompany(providers, selectedCompany)}
              size={18}
              decorative
              className="shrink-0"
            />
            <div className="min-w-0">
              <p className="text-sm font-medium truncate" style={{ color: 'var(--color-secondary)' }}>
                {selectedCompany}
              </p>
              <p className="text-xs truncate" style={{ color: 'var(--color-muted)' }}>
                {[
                  planLabel(selectedPlan),
                  hasRegionForPlan && selectedRegion ? regionLabel(selectedRegion) : null,
                  resolvedEntry ? catalogEndpointHint(resolvedEntry) : null,
                ]
                  .filter(Boolean)
                  .join(' · ')}
              </p>
            </div>
          </div>
          <button tabIndex={0}
            type="button"
            onClick={() => setPickerOpen(true)}
            className="shrink-0 text-xs font-medium px-2.5 py-1.5 rounded transition-colors"
            style={{ color: 'var(--color-accent)' }}
          >
            Change
          </button>
        </div>
      )}

      {/* ── L1: Search + company grid ───────────────────────────────────── */}
      {(pickerOpen || !selectedCompany) && (
      <div className="space-y-2">
        {/* Search box — spec: >25 items → searchable (NN/g) */}
        <div className="relative">
          <MagnifyingGlass
            size={13}
            className="absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none"
            style={{ color: 'var(--color-muted)' }}
          />
          <Input
            type="search"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search providers… (e.g. kimi, glm, qwen)"
            className="pl-8 text-sm h-8"
            aria-label="Search providers"
          />
        </div>

        {/* Catalog load state — the picker has nothing to show without the
            registry-fed document (ADR-068 FR-037). T068-20 ships the full
            error state; this is the minimal honest surface. */}
        {catalogError && (
          <div
            className="rounded-lg border px-3 py-2 flex items-center justify-between gap-2 text-xs"
            style={{ borderColor: 'var(--color-error)', color: 'var(--color-error)' }}
            role="alert"
            data-testid="catalog-error"
          >
            <span>Provider catalog unavailable.</span>
            <button tabIndex={0} type="button" onClick={onRetryCatalog} className="underline">
              Retry
            </button>
          </div>
        )}
        {catalogLoading && !catalogError && (
          <p className="text-xs" style={{ color: 'var(--color-muted)' }} data-testid="catalog-loading">
            Loading providers…
          </p>
        )}

        {/* Company tiles grid */}
        <div className="grid grid-cols-2 sm:grid-cols-3 gap-2" role="group" aria-label="Choose your provider">
          {filteredCompanies.map((company) => {
            const isSelected = selectedCompany === company
            const multi = isMultiVariant(providers, company)
            const slug = logoSlugForCompany(providers, company)
            return (
              <button tabIndex={0}
                key={company}
                type="button"
                onClick={() => {
                  onSelectCompany(company)
                  setPickerOpen(false)
                }}
                className="px-3 py-2.5 rounded-lg border text-sm font-medium transition-all duration-150 text-left flex items-center justify-between gap-1"
                aria-pressed={isSelected}
                style={
                  isSelected
                    ? {
                        borderColor: 'var(--color-accent)',
                        backgroundColor: 'rgba(212,175,55,0.09)',
                        color: 'var(--color-accent)',
                      }
                    : {
                        borderColor: 'var(--color-border)',
                        backgroundColor: 'var(--color-surface-1)',
                        color: 'var(--color-secondary)',
                      }
                }
              >
                <span className="flex items-center gap-1.5 min-w-0">
                  <BrandIcon slug={slug} size={16} decorative className="shrink-0" />
                  <span className="truncate">{company}</span>
                </span>
                {multi && (
                  <CaretDown
                    size={11}
                    weight="bold"
                    className="shrink-0 opacity-60"
                    aria-hidden
                  />
                )}
              </button>
            )
          })}
          {filteredCompanies.length === 0 && (
            <p
              className="col-span-2 sm:col-span-3 text-xs text-center py-2"
              style={{ color: 'var(--color-muted)' }}
            >
              No providers match &ldquo;{searchQuery}&rdquo;
            </p>
          )}
        </div>
      </div>
      )}

      {/* ── L2: Plan + Region (inline, only for multi-variant companies) ── */}
      <AnimatePresence>
        {selectedCompany && multiVariant && (
          <motion.div
            key="plan-region"
            initial={{ opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: 'auto' }}
            exit={{ opacity: 0, height: 0 }}
            transition={{ duration: 0.18 }}
            className="overflow-hidden"
          >
            <div
              className="rounded-lg border p-3 space-y-3"
              style={{
                borderColor: 'var(--color-border)',
                backgroundColor: 'var(--color-surface-1)',
              }}
            >
              {/* Plan selector */}
              <div>
                <p
                  className="text-xs font-medium mb-1.5"
                  style={{ color: 'var(--color-muted)' }}
                >
                  Plan
                </p>
                <div className="flex flex-wrap gap-1.5" role="group" aria-label="Select plan">
                  {companyPlans.map((plan) => (
                    <button tabIndex={0}
                      key={plan}
                      type="button"
                      onClick={() => onSelectPlan(plan)}
                      className="px-2.5 py-1 rounded text-xs font-medium transition-all duration-150"
                      aria-pressed={selectedPlan === plan}
                      style={
                        selectedPlan === plan
                          ? {
                              backgroundColor: 'var(--color-accent)',
                              color: 'var(--color-primary)',
                              fontWeight: 700,
                            }
                          : {
                              borderColor: 'var(--color-border)',
                              border: '1px solid',
                              backgroundColor: 'transparent',
                              color: 'var(--color-secondary)',
                            }
                      }
                    >
                      {planLabel(plan)}
                    </button>
                  ))}
                </div>
                {/* Anti-1113 helper — error-prevention copy (spec §"Error prevention") */}
                <div
                  className="flex items-start gap-1.5 mt-2 text-xs leading-snug"
                  style={{ color: 'var(--color-muted)' }}
                >
                  <Info size={11} className="shrink-0 mt-px" aria-hidden />
                  <span>
                    <strong style={{ color: 'var(--color-secondary)' }}>Coding Plan</strong>{' '}
                    = your subscription (separate billing).{' '}
                    <strong style={{ color: 'var(--color-secondary)' }}>
                      Pay-as-you-go API
                    </strong>{' '}
                    bills per token. Wrong plan returns &ldquo;insufficient balance&rdquo;.
                  </span>
                </div>
              </div>

              {/* Region selector — only when the chosen plan has a regional split */}
              {hasRegionForPlan && (
                <div>
                  <p
                    className="text-xs font-medium mb-1.5"
                    style={{ color: 'var(--color-muted)' }}
                  >
                    Region
                  </p>
                  <div className="flex flex-wrap gap-1.5" role="group" aria-label="Select region">
                    {currentPlanRegions.map((region) => (
                      <button tabIndex={0}
                        key={region}
                        type="button"
                        onClick={() => onSelectRegion(region)}
                        className="px-2.5 py-1 rounded text-xs font-medium transition-all duration-150"
                        aria-pressed={selectedRegion === region}
                        style={
                          selectedRegion === region
                            ? {
                                backgroundColor: 'var(--color-accent)',
                                color: 'var(--color-primary)',
                                fontWeight: 700,
                              }
                            : {
                                borderColor: 'var(--color-border)',
                                border: '1px solid',
                                backgroundColor: 'transparent',
                                color: 'var(--color-secondary)',
                              }
                        }
                      >
                        {regionLabel(region)}
                      </button>
                    ))}
                  </div>
                </div>
              )}

              {/* Endpoint hint/subtitle derived from the fetched catalog entry. */}
              {resolvedEntry && (
                <p className="text-xs truncate" style={{ color: 'var(--color-muted)' }}>
                  {catalogSubtitle(resolvedEntry)}
                </p>
              )}

              {/* Resolved endpoint hint — recognition + debuggability (spec requirement) */}
              {resolvedEntry && (
                <p className="text-xs font-mono" style={{ color: 'var(--color-muted)' }}>
                  → {catalogEndpointHint(resolvedEntry)}
                </p>
              )}
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* ── API key — animates in when a provider is resolved ────────────── */}
      <AnimatePresence>
        {selectedProvider && (
          <motion.div
            key="apikey"
            initial={{ opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: 'auto' }}
            exit={{ opacity: 0, height: 0 }}
            transition={{ duration: 0.2 }}
            className="overflow-hidden"
          >
            <div className="space-y-4">
              <div>
                <label
                  htmlFor="onboarding-api-key"
                  className="text-xs font-medium mb-1.5 block"
                  style={{ color: 'var(--color-muted)' }}
                >
                  API Key
                </label>
                <div className="relative">
                  <Input
                    id="onboarding-api-key"
                    type={showKey ? 'text' : 'password'}
                    value={apiKey}
                    onChange={(e) => onApiKeyChange(e.target.value)}
                    placeholder={providerHint}
                    className="pr-9 font-mono text-sm"
                    autoComplete="off"
                    autoFocus
                  />
                  <button tabIndex={0}
                    type="button"
                    onClick={onToggleShowKey}
                    className={EYE_TOGGLE_CLASS}
                    style={{ color: 'var(--color-muted)' }}
                    aria-label={showKey ? 'Hide API key' : 'Show API key'}
                  >
                    {showKey ? <EyeSlash size={14} /> : <Eye size={14} />}
                  </button>
                </div>
                <p
                  className="text-xs mt-1.5 font-mono"
                  style={{ color: 'var(--color-muted)' }}
                >
                  Stored encrypted with AES-256-GCM — never in plaintext
                </p>
              </div>

              {/* Endpoint input — required for providers with no fixed default
                  base (e.g. Azure, where each resource has its own host). */}
              {requiresEndpoint && (
                <div>
                  <label
                    htmlFor="onboarding-endpoint"
                    className="text-xs font-medium mb-1.5 block"
                    style={{ color: 'var(--color-muted)' }}
                  >
                    API Endpoint <span style={{ color: 'var(--color-error)' }}>*</span>
                  </label>
                  <Input
                    id="onboarding-endpoint"
                    type="url"
                    value={endpoint}
                    onChange={(e) => onEndpointChange(e.target.value)}
                    placeholder="https://<resource>.openai.azure.com/openai/deployments/<deployment>"
                    className="font-mono text-sm"
                    autoComplete="off"
                  />
                  <p className="text-xs mt-1.5" style={{ color: 'var(--color-muted)' }}>
                    {selectedProvider === 'azure' || selectedProvider === 'azure-openai'
                      ? 'Your Azure OpenAI resource URL (per-deployment endpoint)'
                      : 'Custom base URL for this provider'}
                  </p>
                </div>
              )}

              {/* Connection feedback — friendly, actionable message at the display
                  layer; the raw upstream string is preserved behind a collapsible
                  "Technical details" disclosure. */}
              {testStatus === 'error' && (
                <div
                  data-testid="onboarding-error"
                  role="alert"
                  aria-live="assertive"
                  className="flex items-start gap-2 text-sm"
                  style={{ color: 'var(--color-error)' }}
                >
                  <XCircle size={14} weight="fill" className="shrink-0 mt-0.5" />
                  <div className="min-w-0 space-y-1">
                    <span>
                      <span className="sr-only">Error: </span>
                      {friendlyProbeError(testError, providerDisplayName)}
                    </span>
                    {testError && (
                      <details className="text-xs" style={{ color: 'var(--color-muted)' }}>
                        <summary tabIndex={0} className="cursor-pointer select-none">
                          Technical details
                        </summary>
                        <p className="mt-1 font-mono break-words">{testError}</p>
                      </details>
                    )}
                  </div>
                </div>
              )}

              {/* Connect & Load Models CTA */}
              {testStatus !== 'success' && (
                <Button
                  className="w-full gap-2 font-headline font-bold"
                  onClick={onTest}
                  disabled={connectDisabled}
                >
                  {testStatus === 'testing' ? (
                    <>
                      <SpinnerGap size={13} className="animate-spin" />
                      Connecting...
                    </>
                  ) : testStatus === 'error' ? (
                    'Retry Connection'
                  ) : (
                    'Connect & Load Models'
                  )}
                </Button>
              )}

              {/* Model selection — appears after successful connection */}
              <AnimatePresence>
                {testStatus === 'success' && (
                  <motion.div
                    key="model-select"
                    initial={{ opacity: 0, height: 0 }}
                    animate={{ opacity: 1, height: 'auto' }}
                    exit={{ opacity: 0, height: 0 }}
                    transition={{ duration: 0.2 }}
                    className="overflow-hidden space-y-3"
                  >
                    <div
                      className="flex items-center gap-2 text-sm"
                      style={{ color: 'var(--color-success)' }}
                    >
                      <CheckCircle size={14} weight="fill" />
                      <span>Connected successfully</span>
                    </div>

                    <div>
                      <label
                        className="text-xs font-medium mb-1.5 block"
                        style={{ color: 'var(--color-muted)' }}
                      >
                        Default Model <span style={{ color: 'var(--color-error)' }}>*</span>
                      </label>
                      {/* UAT model-catalog fix: constrained picker. When the
                          provider returned a live model list the user picks
                          from it (no free-text). When it has no listing
                          endpoint (empty list) we allow free-text so the user
                          can seed the first slug — but never flag it as
                          "unresolved". */}
                      <ModelSelector
                        models={availableModels}
                        value={selectedModel}
                        onChange={onSelectModel}
                        providerGroups={providerGroups}
                        constrainToCatalog
                        allowFreeTextWhenEmpty
                      />
                      <p className="text-xs mt-1.5" style={{ color: 'var(--color-muted)' }}>
                        {availableModels.length > 0
                          ? 'This model will be used by default for agent tasks'
                          : 'Enter the model slug for this provider (e.g. MiniMax-M2.7)'}
                      </p>
                    </div>
                  </motion.div>
                )}
              </AnimatePresence>
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* Probe-validation warning banner — shown when the connection probe
          succeeded with a non-blocking warning (no_credit / unreachable /
          restricted). The key is accepted; the user can proceed. */}
      {testStatus === 'success' && probeValidation && (
        <ProviderValidationBanner
          validation={probeValidation}
          data-testid="onboarding-probe-validation-banner"
        />
      )}

      {/* Inline finish failure — keeps the user on this step so they can retry. */}
      {finishError && (
        <div
          role="alert"
          data-testid="onboarding-error"
          className="flex items-start gap-2 text-sm text-left"
          style={{ color: 'var(--color-error)' }}
        >
          <XCircle size={14} weight="fill" className="shrink-0 mt-0.5" />
          <span>{finishError}</span>
        </div>
      )}

      {/* Trademark disclaimer — required when BrandIcon logos are shown */}
      <BrandDisclaimer />

      {/* Navigation */}
      <div className="flex items-center gap-3 pt-2">
        <Button
          variant="ghost"
          className="gap-1.5 min-h-11 sm:min-h-0"
          onClick={onBack}
          disabled={isSaving}
        >
          <ArrowLeft size={14} />
          Back
        </Button>
        <Button
          variant={continueEnabled ? 'default' : 'outline'}
          className="flex-1 gap-2 font-headline font-bold"
          onClick={onComplete}
          disabled={!continueEnabled || isSaving}
        >
          {isSaving ? (
            <>
              <SpinnerGap size={14} className="animate-spin" />
              Setting up...
            </>
          ) : finishError ? (
            <>
              Retry Setup
              <ArrowRight size={14} weight="bold" />
            </>
          ) : (
            <>
              Complete Setup
              <ArrowRight size={14} weight="bold" />
            </>
          )}
        </Button>
      </div>
    </div>
  )
}

// ── Completion: Meet your Assistant ────────────────────────────────────────────
//
// Shown after completeOnboardingTransaction succeeds. Introduces Mia — the
// default ⭐ Assistant agent — who was auto-provisioned by coreagent.SeedConfig
// at gateway boot (pkg/gateway/gateway.go). This screen is NOT a numbered step,
// so the step indicator is hidden while it is shown.

function MeetAssistantStep({ onStartChatting }: { onStartChatting: () => void }) {
  return (
    <div className="flex flex-col items-center text-center gap-8">
      {/* Mascot with Forge Gold glow halo */}
      <motion.div
        initial={{ scale: 0.75, opacity: 0 }}
        animate={{ scale: 1, opacity: 1 }}
        transition={{ duration: 0.5, ease: [0.34, 1.56, 0.64, 1] }}
        className="relative"
      >
        <div
          aria-hidden
          className="absolute rounded-full blur-3xl pointer-events-none"
          style={{
            inset: '-40%',
            background: 'rgba(212,175,55,0.14)',
          }}
        />
        <img
          src={OmnipusAvatar}
          alt="omnipus.ai mark"
          className="relative h-28 w-28 sm:h-36 sm:w-36 drop-shadow-2xl"
        />
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.18, duration: 0.38 }}
      >
        <div className="flex items-center justify-center gap-2 mb-2">
          <h1 className="font-headline text-3xl sm:text-4xl font-bold leading-tight"
            style={{ color: 'var(--color-secondary)' }}>
            Mia — Assistant
          </h1>
          <Star
            size={22}
            weight="fill"
            style={{ color: 'var(--color-accent)' }}
            aria-label="Default agent"
          />
        </div>
        <p className="font-headline text-sm font-bold tracking-wide"
          style={{ color: 'var(--color-accent)' }}>
          Your personal Assistant
        </p>
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.28, duration: 0.38 }}
        className="w-full"
      >
        <div
          className="flex items-start gap-3 p-3 rounded-lg border text-left"
          style={{
            borderColor: 'var(--color-border)',
            backgroundColor: 'var(--color-surface-1)',
          }}
        >
          <User size={17} weight="duotone" className="shrink-0 mt-0.5"
            style={{ color: 'var(--color-accent)' }} />
          <p className="text-sm leading-snug" style={{ color: 'var(--color-muted)' }}>
            Your personal Assistant — memory-rich, cross-workspace recall, runs your
            tasks, email, and calendar.
          </p>
        </div>
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.38, duration: 0.38 }}
        className="w-full"
      >
        <p className="text-sm leading-relaxed mb-6" style={{ color: 'var(--color-muted)' }}>
          Mia is your default agent. She&apos;s bound to My Workspace and knows you
          across all your workspaces. Start chatting to begin.
        </p>
        <Button
          className="w-full h-11 gap-2 font-headline font-bold text-base"
          onClick={onStartChatting}
        >
          <ChatCircle size={16} weight="fill" />
          Start chatting
        </Button>
      </motion.div>
    </div>
  )
}

export const Route = createFileRoute('/onboarding')({
  beforeLoad: async () => {
    try {
      const state = await fetchAppState()
      if (state?.onboarding_complete) {
        throw redirect({ to: '/' })
      }
      return { appStateBannerMessage: null as string | null }
    } catch (err) {
      // Re-throw redirect so we don't swallow navigation errors.
      if (err && typeof err === 'object' && 'to' in err) throw err
      // Log non-redirect errors so operators can diagnose fetch failures.
      console.error('onboarding.app_state_fetch_failed', err)
      // Surface a visible banner for 5xx server errors. Network failures still
      // allow the wizard to proceed (fresh install with broken /about endpoint).
      const bannerMessage =
        isApiError(err) && err.status >= 500
          ? `Could not load setup state — server returned ${err.status}`
          : null
      return { appStateBannerMessage: bannerMessage }
    }
  },
  component: OnboardingWizard,
})
