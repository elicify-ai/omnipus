import { useState, useMemo, useRef } from 'react'
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
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ModelSelector, type ModelCatalogGroup } from '@/components/ui/model-selector'
import { probeProvider, completeOnboardingTransaction, fetchAppState, isApiError } from '@/lib/api'
import { providersCatalogQueryOptions } from '@/lib/providersCatalogQuery'
import OmnipusAvatar from '@/assets/logo/omnipus-avatar.svg?url'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'
import { queryClient } from '@/lib/queryClient'
import { ProviderValidationBanner } from '@/components/providers/ProviderValidationBanner'
import { ProviderPicker, type PickerSelection } from '@/components/providers/ProviderPicker'
import type { ProviderDetailSelection } from '@/components/providers/ProviderDetailPanel'
import { SignInDialog } from '@/components/providers/SignInDialog'
import type { AuthMethod } from '@/components/providers/AuthMethodControl'
import type {
  CatalogProvider,
  OnboardingProviderApiKey,
  OnboardingProviderSignIn,
  ProbeProviderRequest,
  ProviderValidation,
  ProvidersCatalog,
} from '@/lib/api/generated/openapi-types'
import { BrandIcon } from '@/components/ui/brand-icon'
import { BrandDisclaimer } from '@/components/ui/brand-disclaimer'
import { PLAN_LABELS, REGION_LABELS } from '@/lib/providerLabels'
import {
  catalogEndpointHint,
  catalogEntryById,
  catalogLogoSlug,
  catalogSubtitle,
} from '@/lib/catalogDisplay'

// First-launch onboarding flow — full-screen, outside AppShell.
//
// Spec-6 FR-12.3: three numbered steps — name → password → model key — followed
// by an unnumbered "Meet your Assistant" completion screen that introduces Mia
// (the default ⭐ Assistant agent, auto-provisioned by coreagent.SeedConfig at
// gateway boot). The step indicator tracks the 3 numbered steps only; the
// completion screen is not a numbered step.

type Step = 1 | 2 | 3
type TestStatus = 'idle' | 'testing' | 'success' | 'error'

// ── Provider data model (ADR-068 FR-037/FR-021) ──────────────────────────────
//
// The picker sources from the registry-fed catalog the gateway serves at
// GET /api/v1/providers/catalog (src/lib/api.ts::fetchProvidersCatalog) —
// there is NO bundled catalog (SC-010). Onboarding step 3 renders the ONE
// shared `ProviderPicker` (first level: Popular tiles / letter-grouped list /
// Custom endpoint) and its `ProviderDetailPanel` second level (plan, region,
// auth method), the same pair Settings → Providers renders.

// Providers that REQUIRE a custom endpoint to function. The probe will always
// return "unknown provider" for these without an endpoint because no fixed
// default base exists (e.g. Azure is per-resource-host).
export const PROVIDERS_REQUIRING_ENDPOINT = new Set(['azure', 'azure-openai'])

// Plan/region labels live in src/lib/providerLabels.ts (shared with Settings'
// ProvidersSection, provider-ux-fixes-plan FIX-4) — re-exported here so
// existing `from './onboarding'` imports (incl. tests) keep working.
export { PLAN_LABELS, REGION_LABELS }

/**
 * FR-029, verbatim: the model field's accessible label on onboarding step 3.
 * Exported so the test asserts the shipped string rather than a copy of it.
 */
export const ONBOARDING_MODEL_LABEL = 'Model for your first agent'

// ── Catalog helpers ───────────────────────────────────────────────────────────
//
// ADR-068 T068-24 replaced this file's own two-level company grid with the ONE
// shared picker (`ProviderPicker` + `ProviderDetailPanel`, FR-021), so the
// company/plan/region derivation that used to live here now lives in
// `provider-picker-model.ts` and is exercised by the picker's own tests. What
// onboarding still needs from the catalog is a single lookup: the row behind
// the id the picker handed back, so the confirmed-selection summary can show
// the same catalog-derived subtitle and endpoint hint Settings shows (US-7
// parity, asserted in onboarding-settings-parity.test.tsx). That lookup is
// `catalogEntryById` in `@/lib/catalogDisplay` — one exact-match helper shared
// with Settings (ADR-067 FR-011), never a second copy here.

/**
 * The CLI binary name a sign-in row drives (`codex`, `copilot`), used for the
 * FR-036 / §8b hint *"`codex` not found on this machine"*. Undefined for a
 * sign-in row that is not CLI-backed (device-code providers own no binary).
 */
export function cliBinaryName(entry: CatalogProvider | undefined): string | undefined {
  return entry?.cli_kind
}

/**
 * True when a failed sign-in probe failed because the vendor CLI is not on
 * PATH, rather than because nobody is signed in. The gateway reports the two
 * differently in prose; the SPA must not conflate them, because the fix is
 * different (install the CLI vs run its login command).
 */
export function probeErrorIsMissingCli(message: string): boolean {
  return /not found|not installed|no such file|executable file not found/i.test(message)
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

/**
 * What step 3 holds once the picker's second-level panel is confirmed — one
 * configurable provider row plus whatever the operator typed for it. A custom
 * endpoint row carries `apiBase`/`protocol` and no catalog entry; a catalog row
 * carries the entry and neither.
 */
type ProviderSelection = {
  providerId: string
  authMethod: AuthMethod
  /** The catalog row, when the id is a catalog id. Undefined for a custom row. */
  entry?: CatalogProvider
  /** Typed key — only ever set on the `api_key` path. */
  apiKey: string
  apiBase?: string
  protocol?: 'openai-compatible' | 'anthropic'
  /** What the summary calls it: the company for a catalog row, the id otherwise. */
  displayName: string
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
  // Step 3 — the confirmed provider row (null while the picker is open).
  const [selection, setSelection] = useState<ProviderSelection | null>(null)
  // Step 3 sign-in (ADR-068 §8b, FR-045/FR-050) — the SAME SignInDialog
  // Settings → Providers uses. FR-050 makes the five sign-in routes reachable
  // while onboarding is incomplete, which is what lets an operator sign in
  // here before any admin account exists to authenticate as.
  const [signInTarget, setSignInTarget] = useState<{ id: string; label: string } | null>(null)
  const [signInOpen, setSignInOpen] = useState(false)
  // FR-029: no model is pre-selected, ever. This starts empty and only the
  // operator's own pick fills it.
  const [selectedModel, setSelectedModel] = useState('')
  const [probeStatus, setProbeStatus] = useState<TestStatus>('idle')
  const [probeError, setProbeError] = useState('')
  // The model the LAST successful probe actually exercised (FR-036's
  // `probed_model`). Finish compares it to the current pick, so a probe that
  // passed for a DIFFERENT model can never enable Finish.
  const [probedModel, setProbedModel] = useState('')
  const [isSaving, setIsSaving] = useState(false)
  // Surfaced inline on step 3 when completeOnboardingTransaction fails, so the
  // user stays on the step and can retry rather than failing silently.
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

  // Monotonic probe id. Changing the model re-probes (FR-029) and a slow first
  // response must never overwrite a newer one — that is how a passing probe for
  // an abandoned model would enable Finish for the model on screen.
  const probeSeq = useRef(0)

  const goTo = (next: Step) => {
    setDirection(next > step ? 1 : -1)
    setStep(next)
  }

  const resetProbe = () => {
    probeSeq.current += 1
    setProbeStatus('idle')
    setProbeError('')
    setProbedModel('')
    setProbeValidation(undefined)
  }

  /**
   * FR-029 — probe the CHOSEN auth method for the CHOSEN model. `api_key`
   * sends the typed key; `sign_in` sends no key at all and the gateway goes
   * through the CLI's saved login / Copilot session (CRIT-002).
   */
  const runProbe = async (model: string, current: ProviderSelection | null = selection) => {
    if (!current) return
    const seq = ++probeSeq.current
    setProbeStatus('testing')
    setProbeError('')
    setProbeValidation(undefined)
    try {
      const req: ProbeProviderRequest = {
        id: current.providerId,
        auth: current.authMethod,
        ...(current.authMethod === 'api_key' ? { api_key: current.apiKey } : {}),
        ...(model.trim() ? { model: model.trim() } : {}),
        ...(current.apiBase ? { api_base: current.apiBase } : {}),
        ...(current.protocol ? { protocol: current.protocol } : {}),
      }
      const result = await probeProvider(req)
      // A stale response (the operator changed the model while it was in
      // flight) is dropped, not applied.
      if (seq !== probeSeq.current) return
      if (result.success) {
        setProbeStatus('success')
        setProbedModel(result.probed_model ?? model.trim())
        if (result.validation && result.validation.outcome !== 'valid') {
          setProbeValidation(result.validation)
        }
      } else {
        setProbeStatus('error')
        setProbeError(result.error ?? 'Connection test failed')
      }
    } catch (err) {
      if (seq !== probeSeq.current) return
      setProbeStatus('error')
      setProbeError(err instanceof Error ? err.message : String(err))
    }
  }

  /**
   * Opens the sign-in dialog for a `sign_in` catalog row — from the picker's
   * second-level *Sign in* button, or from the confirmed summary row.
   */
  const openSignInDialog = (providerId: string, label?: string) => {
    const entry = catalogEntryById(providers, providerId)
    setSignInTarget({ id: providerId, label: label ?? entry?.name ?? entry?.company ?? providerId })
    setSignInOpen(true)
  }

  /**
   * A completed sign-in changes what the probe would say. Re-run it for the
   * model already on screen so *Finish* reflects the new session without the
   * operator having to re-pick the model — FR-036's gate is unchanged: Finish
   * still needs a PASSED probe, and a sign_in probe passes only when the
   * gateway reports the session as signed in (400 `field=auth` otherwise).
   */
  const handleSignedIn = () => {
    if (selectedModel.trim()) void runProbe(selectedModel)
  }

  /** The picker's second-level panel confirmed a catalog row (FR-027/FR-028). */
  const handleProviderConfirm = (confirmed: ProviderDetailSelection) => {
    const entry = catalogEntryById(providers, confirmed.providerId)
    setSelection({
      providerId: confirmed.providerId,
      authMethod: confirmed.authMethod,
      entry,
      apiKey: confirmed.apiKey ?? '',
      displayName: entry?.company ?? confirmed.providerId,
    })
    setSelectedModel('')
    resetProbe()
    setFinishError('')
  }

  /**
   * The picker's first-level selections. Only the Custom endpoint row settles
   * anything on its own — a tile or list row opens the second-level panel and
   * arrives here again through `handleProviderConfirm`. FR-037: this is the
   * path onboarding takes when the catalog GET failed entirely.
   */
  const handlePickerSelect = (picked: PickerSelection) => {
    if (picked.kind !== 'custom') return
    setSelection({
      providerId: picked.draft.id,
      authMethod: 'api_key',
      entry: catalogEntryById(providers, picked.draft.id),
      apiKey: picked.draft.api_key,
      apiBase: picked.draft.api_base,
      protocol: picked.draft.protocol,
      displayName: picked.draft.id,
    })
    setSelectedModel('')
    resetProbe()
    setFinishError('')
  }

  /** *Change* — back to the picker, with nothing carried over. */
  const handleChangeProvider = () => {
    setSelection(null)
    setSelectedModel('')
    resetProbe()
    setFinishError('')
  }

  // FR-029: choosing a model probes it; changing it re-probes and Finish goes
  // back to disabled until that probe passes.
  //
  // `autoProbe` is false only on the free-text path (a custom endpoint row has
  // no catalog listing), where the value changes on every keystroke and a probe
  // per keystroke would be a request storm against the operator's provider. The
  // reset still happens, so Finish is disabled the moment the slug changes; the
  // explicit *Check connection* button runs the probe.
  const handleSelectModel = (model: string, autoProbe: boolean) => {
    setSelectedModel(model)
    resetProbe()
    if (autoProbe && model.trim()) void runProbe(model)
  }

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
    if (!selection) return
    setIsSaving(true)
    setFinishError('')
    try {
      // FR-035 / MAJ-014: the body is the generated discriminated union. The
      // sign_in variant HAS no api_key property — sending one is a 400, so the
      // two variants are built separately rather than by deleting a field.
      const provider: OnboardingProviderApiKey | OnboardingProviderSignIn =
        selection.authMethod === 'sign_in'
          ? {
              auth_method: 'sign_in',
              id: selection.providerId,
              model: selectedModel,
              ...(selection.apiBase ? { endpoint: selection.apiBase } : {}),
            }
          : {
              auth_method: 'api_key',
              id: selection.providerId,
              api_key: selection.apiKey,
              model: selectedModel,
              ...(selection.apiBase ? { endpoint: selection.apiBase } : {}),
            }
      const resp = await completeOnboardingTransaction({
        provider,
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
          content overflows (the step-3 picker is taller than a phone viewport)
          — overflow-hidden + justify-center clipped both ends. */}
      <div className="w-full flex flex-col items-center">
      {/* Step indicator — labeled for assistive tech so screen readers announce
          progress. The dots themselves are decorative (aria-hidden); the
          progressbar role + valuenow/min/max + aria-label carry the semantics,
          and the sr-only line gives a plain-text "Step X of N" announcement.
          Hidden on the unnumbered "Meet your Assistant" completion screen.

          FR-028: the auth-method control lives INSIDE step 3's second-level
          panel, so this stays three steps — a fourth numbered step for it is
          exactly what the FR forbids. */}
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
              <ProviderStep
                catalog={catalogDoc}
                catalogLoading={catalogLoading}
                catalogError={catalogError}
                onRetryCatalog={() => { void refetchCatalog() }}
                selection={selection}
                onPickerSelect={handlePickerSelect}
                onProviderConfirm={handleProviderConfirm}
                onSignIn={openSignInDialog}
                onChangeProvider={handleChangeProvider}
                selectedModel={selectedModel}
                onSelectModel={handleSelectModel}
                probeStatus={probeStatus}
                probeError={probeError}
                probedModel={probedModel}
                onReprobe={() => { void runProbe(selectedModel) }}
                onBack={() => goTo(2)}
                onComplete={handleComplete}
                isSaving={isSaving}
                finishError={finishError}
                probeValidation={probeValidation}
              />
            </motion.div>
          )}
        </AnimatePresence>
      </div>
      </div>

      {/* The shared sign-in dialog (T068-33). Rendered at the wizard level, not
          inside the animated step, so a step transition cannot unmount an open
          device-code session mid-poll. */}
      {signInTarget && (
        <SignInDialog
          open={signInOpen}
          onOpenChange={(next) => {
            setSignInOpen(next)
            if (!next) setSignInTarget(null)
          }}
          providerId={signInTarget.id}
          providerLabel={signInTarget.label}
          onSignedIn={handleSignedIn}
        />
      )}
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

// ── Step 3: connect a provider ────────────────────────────────────────────────
//
// ADR-068 FR-021/FR-028/FR-029. The step is the shared `ProviderPicker` until a
// row is confirmed, then a short configuration block: what was chosen, the
// sign-in check where the chosen method is sign-in, and the model field — which
// starts EMPTY and whose every change re-probes.
//
// Why the probe is model-driven rather than a "Connect" button: FR-029 makes
// the probe a property of the pair (auth method x model), not of the key alone.
// A key that works and a model that 404s is the exact failure the old
// "Connect & Load Models" flow shipped to users, because it validated the key
// and then let them pick any slug from the list.

function ProviderStep({
  catalog,
  catalogLoading,
  catalogError,
  onRetryCatalog,
  selection,
  onPickerSelect,
  onProviderConfirm,
  onSignIn,
  onChangeProvider,
  selectedModel,
  onSelectModel,
  probeStatus,
  probeError,
  probedModel,
  onReprobe,
  onBack,
  onComplete,
  isSaving,
  finishError,
  probeValidation,
}: {
  catalog: ProvidersCatalog | undefined
  catalogLoading: boolean
  catalogError: boolean
  onRetryCatalog: () => void
  selection: ProviderSelection | null
  onPickerSelect: (selection: PickerSelection) => void
  onProviderConfirm: (selection: ProviderDetailSelection) => void
  onSignIn: (providerId: string, label?: string) => void
  onChangeProvider: () => void
  selectedModel: string
  onSelectModel: (model: string, autoProbe: boolean) => void
  probeStatus: TestStatus
  probeError: string
  probedModel: string
  onReprobe: () => void
  onBack: () => void
  onComplete: () => void
  isSaving: boolean
  finishError: string
  probeValidation?: ProviderValidation
}) {
  const entry = selection?.entry
  const catalogModels = entry?.models ?? []
  // Catalog mode gives the FR-030 ordering and Recommended chips; a custom
  // endpoint row has no catalog listing at all, so it falls back to typing the
  // slug (and to the explicit check button below rather than a probe per
  // keystroke).
  const hasCatalogModels = catalogModels.length > 0
  const catalogGroups: ModelCatalogGroup[] | undefined = hasCatalogModels
    ? [
        {
          providerId: selection?.providerId ?? '',
          providerName: selection?.displayName ?? '',
          models: catalogModels,
        },
      ]
    : undefined

  const signIn = selection?.authMethod === 'sign_in'
  // ADR-067 FR-039 — a `locality: local` row (Ollama, vLLM, LM Studio) needs
  // no credential; ProviderDetailPanel already substitutes
  // LOCAL_PROVIDER_CREDENTIAL for the typed key on this path, so this only
  // drives copy — never the Finish gate below (see keyMissing).
  const isLocal = entry?.locality === 'local'
  const missingCli =
    signIn && probeStatus === 'error' && probeErrorIsMissingCli(probeError)
      ? cliBinaryName(entry)
      : undefined

  // FR-029: Finish needs a model AND a passed probe FOR THAT MODEL.
  const modelChosen = selectedModel.trim() !== ''
  const finishEnabled =
    !!selection && modelChosen && probeStatus === 'success' && probedModel === selectedModel.trim()

  const providerDisplayName = selection?.displayName ?? 'the provider'
  const needsCustomEndpoint =
    !!selection && PROVIDERS_REQUIRING_ENDPOINT.has(selection.providerId) && !selection.apiBase
  // Error prevention (the invariant the retired "Connect is disabled when the
  // API key is empty" test carried): a key-path probe with no key can only come
  // back as an upstream auth failure the operator cannot act on, so it is never
  // sent — not on a model pick, not from the button. `isLocal` is excluded
  // defensively even though ProviderDetailPanel never leaves `apiKey` empty
  // for a local row — this gate must never re-block Ollama/vLLM/LM Studio if
  // that invariant ever slips.
  const keyMissing = !!selection && !signIn && !isLocal && selection.apiKey.trim() === ''
  const probeBlocked = keyMissing || needsCustomEndpoint

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

      {/* ── The ONE picker (FR-021) ──────────────────────────────────────── */}
      {!selection && (
        <ProviderPicker
          data-testid="onboarding-provider-picker"
          catalog={catalog}
          status={catalogLoading ? 'loading' : catalogError ? 'error' : 'ready'}
          onRetry={onRetryCatalog}
          onSelect={onPickerSelect}
          onProviderConfirm={onProviderConfirm}
          onSignIn={onSignIn}
          autoFocus={false}
        />
      )}

      {/* ── Confirmed row: what was chosen, straight from the catalog ────── */}
      {selection && (
        <div
          data-testid="onboarding-provider-summary"
          className="rounded-lg border p-3 flex items-center justify-between gap-2"
          style={{ borderColor: 'var(--color-accent)', backgroundColor: 'rgba(212,175,55,0.06)' }}
        >
          <div className="flex items-center gap-2 min-w-0">
            {entry && (
              <BrandIcon slug={catalogLogoSlug(entry)} size={18} decorative className="shrink-0" />
            )}
            <div className="min-w-0">
              <p className="text-sm font-medium truncate" style={{ color: 'var(--color-secondary)' }}>
                {selection.displayName}
              </p>
              {/* US-7 parity: the same catalogDisplay derivation Settings uses. */}
              {entry && (
                <>
                  <p className="text-xs truncate" style={{ color: 'var(--color-muted)' }}>
                    {catalogSubtitle(entry)}
                  </p>
                  <p className="text-xs font-mono truncate" style={{ color: 'var(--color-muted)' }}>
                    → {catalogEndpointHint(entry)}
                  </p>
                </>
              )}
              {selection.apiBase && (
                <p className="text-xs font-mono truncate" style={{ color: 'var(--color-muted)' }}>
                  → {selection.apiBase}
                </p>
              )}
              <p className="text-xs" style={{ color: 'var(--color-muted)' }}>
                {signIn ? 'Signed in with the provider' : isLocal ? 'No key needed — runs locally' : 'API key'}
              </p>
              {/* FR-045: the sign-in path has nothing to type — the dialog is
                  the whole interaction, and it must be reachable again after
                  the row is confirmed (a session can lapse mid-onboarding). */}
              {signIn && (
                <button
                  type="button"
                  tabIndex={0}
                  data-testid="onboarding-sign-in-btn"
                  onClick={() => onSignIn(selection.providerId, selection.displayName)}
                  className="mt-1 text-xs font-medium px-2 py-1 rounded border"
                  style={{ borderColor: 'var(--color-accent)', color: 'var(--color-accent)' }}
                >
                  Sign in
                </button>
              )}
            </div>
          </div>
          <button
            type="button"
            tabIndex={0}
            onClick={onChangeProvider}
            className="shrink-0 text-xs font-medium px-2.5 py-1.5 rounded transition-colors"
            style={{ color: 'var(--color-accent)' }}
          >
            Change
          </button>
        </div>
      )}

      {/* A provider with no fixed default base cannot be reached from its
          catalog row alone — say so instead of letting the probe fail with an
          upstream error nobody can act on. */}
      {needsCustomEndpoint && (
        <p
          data-testid="onboarding-needs-endpoint"
          className="text-xs"
          style={{ color: 'var(--color-warning)' }}
        >
          {selection?.displayName} needs a per-resource endpoint. Go back and use{' '}
          <strong>Custom endpoint</strong> to enter it.
        </p>
      )}

      {selection && (
        <div className="space-y-4">
          {/* ── Model — empty, labelled, and the probe's subject (FR-029) ─── */}
          <div className="space-y-1.5">
            <ModelSelector
              label={ONBOARDING_MODEL_LABEL}
              triggerTestId="onboarding-model-select"
              itemTestIdPrefix="onboarding-model-"
              models={hasCatalogModels ? catalogModels.map((m) => m.id) : []}
              catalogGroups={catalogGroups}
              value={selectedModel}
              onChange={(model) => onSelectModel(model, hasCatalogModels && !probeBlocked)}
              constrainToCatalog={hasCatalogModels}
              allowFreeTextWhenEmpty
            />
            <p className="text-xs" style={{ color: 'var(--color-muted)' }}>
              {hasCatalogModels
                ? 'Your first agent starts on this model. You can change it later.'
                : 'Enter the model slug for this provider (e.g. MiniMax-M2.7)'}
            </p>
          </div>

          {/* ── The probe of the chosen auth method ──────────────────────── */}
          {probeStatus === 'testing' && (
            <p
              data-testid="onboarding-probe-status"
              role="status"
              className="flex items-center gap-2 text-sm"
              style={{ color: 'var(--color-muted)' }}
            >
              <SpinnerGap size={13} className="animate-spin" />
              {signIn ? 'Checking your sign-in…' : 'Checking your key…'}
            </p>
          )}

          {probeStatus === 'success' && (
            <p
              data-testid="onboarding-probe-status"
              role="status"
              className="flex items-center gap-2 text-sm"
              style={{ color: 'var(--color-success)' }}
            >
              <CheckCircle size={14} weight="fill" />
              {signIn
                ? `Signed in — ${probedModel} is ready`
                : `Key accepted — ${probedModel} is ready`}
            </p>
          )}

          {probeStatus === 'error' && (
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
                  {missingCli ? (
                    <span data-testid="onboarding-cli-missing">
                      <code>{missingCli}</code> not found on this machine — install it, then check
                      again.
                    </span>
                  ) : (
                    friendlyProbeError(probeError, providerDisplayName)
                  )}
                </span>
                {probeError && (
                  <details className="text-xs" style={{ color: 'var(--color-muted)' }}>
                    <summary tabIndex={0} className="cursor-pointer select-none">
                      Technical details
                    </summary>
                    <p className="mt-1 font-mono break-words">{probeError}</p>
                  </details>
                )}
              </div>
            </div>
          )}

          {/* The explicit check. For sign-in it is the FR-036 *Check sign-in*
              affordance (nothing to type, so nothing else would trigger a
              probe); for a typed key it is the retry after a failure and the
              trigger for a free-text slug. */}
          {probeStatus !== 'success' && (
            <Button
              variant="outline"
              className="w-full gap-2 font-headline font-bold"
              data-testid="onboarding-probe-button"
              onClick={onReprobe}
              disabled={probeStatus === 'testing' || probeBlocked}
            >
              {probeStatus === 'testing' ? (
                <>
                  <SpinnerGap size={13} className="animate-spin" />
                  Checking…
                </>
              ) : signIn ? (
                'Check sign-in'
              ) : (
                'Check connection'
              )}
            </Button>
          )}

          {keyMissing && (
            <p
              data-testid="onboarding-key-missing"
              className="text-xs"
              style={{ color: 'var(--color-muted)' }}
            >
              Add an API key for {providerDisplayName} — use <strong>Change</strong> to go back to
              the key field.
            </p>
          )}
        </div>
      )}

      {/* Probe-validation warning banner — shown when the probe succeeded with
          a non-blocking warning (no_credit / unreachable / restricted). The
          provider is usable; the user can proceed. */}
      {probeStatus === 'success' && probeValidation && (
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
          variant={finishEnabled ? 'default' : 'outline'}
          className="flex-1 gap-2 font-headline font-bold"
          onClick={onComplete}
          disabled={!finishEnabled || isSaving}
        >
          {isSaving ? (
            <>
              <SpinnerGap size={14} className="animate-spin" />
              Setting up...
            </>
          ) : finishError ? (
            <>
              Retry setup
              <ArrowRight size={14} weight="bold" />
            </>
          ) : (
            <>
              Finish
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
