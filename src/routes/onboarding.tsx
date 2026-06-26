import { useState } from 'react'
import { createFileRoute, redirect, useNavigate, useRouteContext } from '@tanstack/react-router'
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
import { ModelSelector, type ModelGroup } from '@/components/ui/model-selector'
import { probeProvider, completeOnboardingTransaction, fetchAppState, isApiError } from '@/lib/api'
import { pickCapableDefaultModel } from '@/lib/onboarding/defaultModel'
import OmnipusAvatar from '@/assets/logo/omnipus-avatar.svg?url'
import { PROVIDER_HINTS } from '@/lib/constants'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'

// First-launch onboarding flow — full-screen, outside AppShell.
//
// Spec-6 FR-12.3: three numbered steps — name → password → model key — followed
// by an unnumbered "Meet your Assistant" completion screen that introduces Mia
// (the default ⭐ Assistant agent, auto-provisioned by coreagent.SeedConfig at
// gateway boot). The step indicator tracks the 3 numbered steps only; the
// completion screen is not a numbered step.

type Step = 1 | 2 | 3
type TestStatus = 'idle' | 'testing' | 'success' | 'error'

// All supported providers. Providers with /v1/models get a searchable dropdown;
// providers without it get a text input for manual model slug entry.
//
// Dual-endpoint vendors (China vs international) are listed as separate entries
// so users pick the one matching their key/region. The ids below are all valid
// values in the ProbeProviderRequest enum (src/lib/api/generated/).
export const AVAILABLE_PROVIDERS = [
  { id: 'openai', display_name: 'OpenAI' },
  { id: 'openrouter', display_name: 'OpenRouter' },
  { id: 'anthropic', display_name: 'Anthropic' },
  { id: 'google', display_name: 'Google Gemini' },
  { id: 'groq', display_name: 'Groq' },
  { id: 'deepseek', display_name: 'DeepSeek' },
  { id: 'mistral', display_name: 'Mistral' },
  { id: 'azure', display_name: 'Azure OpenAI' },
  // Zhipu / Z.ai: two separate platforms. z-ai = international (api.z.ai);
  // zhipu = China-mainland (open.bigmodel.cn). Pick the one matching your key.
  { id: 'z-ai', display_name: 'Z.ai (GLM, International)' },
  { id: 'zhipu', display_name: 'Zhipu (GLM, China)' },
  // Moonshot / Kimi: api.moonshot.ai (intl) vs api.moonshot.cn (China).
  { id: 'moonshot', display_name: 'Moonshot / Kimi (International)' },
  { id: 'moonshot-cn', display_name: 'Moonshot / Kimi (China)' },
  { id: 'nvidia', display_name: 'NVIDIA' },
  // MiniMax: api.minimax.io (intl) vs api.minimaxi.com (China).
  { id: 'minimax', display_name: 'MiniMax (International)' },
  { id: 'minimax-cn', display_name: 'MiniMax (China)' },
  // Qwen / DashScope: China (dashscope.aliyuncs.com), International, and US
  // are all separate platforms — pick the one matching your Alibaba Cloud region.
  { id: 'qwen', display_name: 'Qwen (China)' },
  { id: 'qwen-intl', display_name: 'Qwen (International)' },
  { id: 'qwen-us', display_name: 'Qwen (US)' },
  { id: 'ollama', display_name: 'Ollama' },
  { id: 'cerebras', display_name: 'Cerebras' },
]

// Providers that REQUIRE a custom endpoint to function. The probe will always
// return "unknown provider" for these without an endpoint because no fixed
// default base exists (e.g. Azure is per-resource-host).
export const PROVIDERS_REQUIRING_ENDPOINT = new Set(['azure', 'azure-openai'])

// Popular providers surfaced first in the onboarding selection grid to reduce
// decision overload (Hick's law). Providers not listed here keep their original
// order via a stable sort (see sortProvidersByPriority).
const PROVIDER_PRIORITY = ['openai', 'anthropic', 'openrouter']

// Stable sort that moves PROVIDER_PRIORITY entries to the front (in priority
// order) and leaves every other provider in its original relative position.
export function sortProvidersByPriority<T extends { id: string }>(list: T[]): T[] {
  const rank = (id: string) => {
    const i = PROVIDER_PRIORITY.indexOf(id)
    return i === -1 ? PROVIDER_PRIORITY.length : i
  }
  return list
    .map((p, index) => ({ p, index }))
    .sort((a, b) => rank(a.p.id) - rank(b.p.id) || a.index - b.index)
    .map(({ p }) => p)
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

  // Always show all available providers in onboarding, regardless of API results
  const providers = AVAILABLE_PROVIDERS

  const [step, setStep] = useState<Step>(1)
  const [direction, setDirection] = useState(1)
  // `completed` flips true once completeOnboardingTransaction succeeds; the
  // numbered step indicator is hidden and the unnumbered "Meet your Assistant"
  // completion screen is rendered instead.
  const [completed, setCompleted] = useState(false)
  const [selectedProvider, setSelectedProvider] = useState('')
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
    try {
      // Non-persistent test + fetch: the server probes the provider with the
      // supplied key and returns the model list in one response. Nothing is
      // saved to disk until the user clicks "Complete setup" on step 3, which
      // fires /onboarding/complete with the full payload atomically.
      const endpointArg = endpoint.trim() || undefined
      const result = await probeProvider(selectedProvider, apiKey.trim(), endpointArg)
      if (result.success) {
        setTestStatus('success')
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
  // store the auth token and reveal the "Meet your Assistant" screen. On
  // failure, surface the error inline on step 3 so the user can retry without
  // losing their place.
  const handleComplete = async () => {
    setIsSaving(true)
    setFinishError('')
    try {
      const resp = await completeOnboardingTransaction({
        provider: {
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
      useAuthStore.getState().setToken(resp.token, resp.role, resp.username)
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
      className="min-h-screen flex flex-col items-center justify-center p-6 relative overflow-hidden"
      style={{ backgroundColor: 'var(--color-primary)', color: 'var(--color-secondary)' }}
    >
      {/* Atmospheric depth — subtle Forge Gold radial glow */}
      <div
        aria-hidden
        className="absolute inset-0 pointer-events-none"
        style={{
          background:
            'radial-gradient(ellipse 65% 55% at 50% 50%, rgba(212,175,55,0.055) 0%, transparent 68%)',
        }}
      />
      {/* Top edge accent line */}
      <div
        aria-hidden
        className="absolute top-0 left-0 right-0 h-px pointer-events-none"
        style={{
          background:
            'linear-gradient(90deg, transparent 0%, rgba(212,175,55,0.35) 50%, transparent 100%)',
        }}
      />

      {/* Server error banner — surfaces when /api/v1/state returned 5xx during beforeLoad */}
      {appStateBannerMessage && (
        <div
          role="alert"
          className="absolute top-4 left-1/2 -translate-x-1/2 w-full max-w-md z-20 rounded-md border border-red-500/40 bg-red-500/10 px-4 py-2.5 text-sm text-red-400"
        >
          {appStateBannerMessage}
        </div>
      )}

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
                selectedProvider={selectedProvider}
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
              />
            </motion.div>
          )}
        </AnimatePresence>
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
          <div data-testid="onboarding-error" className="flex items-start gap-2 text-sm" style={{ color: 'var(--color-error)' }}>
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
            <button
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
              <p className="text-[10px] mt-1 font-medium" style={{ color: strength.color }}>
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
            <button
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
          <div data-testid="onboarding-error" className="flex items-start gap-2 text-sm" style={{ color: 'var(--color-error)' }}>
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
  selectedProvider,
  onSelect,
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
}: {
  providers: { id: string; display_name: string }[]
  selectedProvider: string
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
}) {
  // Order the rendered provider list with the popular providers first (stable).
  const orderedProviders = sortProvidersByPriority(providers)
  // Build providerGroups for the ModelSelector — single group in onboarding (one provider at a time)
  const providerDef = providers.find((p) => p.id === selectedProvider)
  const providerGroups: ModelGroup[] =
    availableModels.length > 0 && providerDef
      ? [{ providerName: providerDef.display_name, models: availableModels }]
      : []
  const continueEnabled = testStatus === 'success' && !!selectedModel.trim()
  const requiresEndpoint = PROVIDERS_REQUIRING_ENDPOINT.has(selectedProvider)
  // The Connect button is blocked when a required endpoint is missing.
  const connectDisabled =
    !apiKey.trim() ||
    testStatus === 'testing' ||
    (requiresEndpoint && !endpoint.trim())
  return (
    <div className="flex flex-col gap-6">
      <div>
        <h2 className="font-headline text-2xl font-bold mb-1"
          style={{ color: 'var(--color-secondary)' }}>
          Add a model key
        </h2>
        <p className="text-sm" style={{ color: 'var(--color-muted)' }}>
          Omnipus needs an AI provider to power your agents.
        </p>
        <p className="text-xs mt-1" style={{ color: 'var(--color-muted)' }}>
          Not sure? OpenAI or OpenRouter are good starting points.
        </p>
      </div>

      {/* Provider selection grid */}
      <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
        {orderedProviders.map((p) => (
          <button
            key={p.id}
            type="button"
            onClick={() => onSelect(p.id)}
            className="px-3 py-2.5 rounded-lg border text-sm font-medium transition-all duration-150 text-left focus-visible:outline-none focus-visible:ring-2"
            style={
              selectedProvider === p.id
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
            {p.display_name}
          </button>
        ))}
      </div>

      {/* API key — animates in when provider is selected */}
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
                  <button
                    type="button"
                    onClick={onToggleShowKey}
                    className={EYE_TOGGLE_CLASS}
                    style={{ color: 'var(--color-muted)' }}
                    aria-label={showKey ? 'Hide API key' : 'Show API key'}
                  >
                    {showKey ? <EyeSlash size={14} /> : <Eye size={14} />}
                  </button>
                </div>
                <p className="text-[10px] mt-1.5 font-mono" style={{ color: 'var(--color-muted)' }}>
                  Stored encrypted with AES-256-GCM — never in plaintext
                </p>
              </div>

              {/* Endpoint input — required for providers with no fixed default
                  base (e.g. Azure, where each resource has its own host). The
                  field is mandatory for PROVIDERS_REQUIRING_ENDPOINT; for others
                  it is not shown (they use the well-known default). */}
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
                  <p className="text-[10px] mt-1.5" style={{ color: 'var(--color-muted)' }}>
                    {selectedProvider === 'azure' || selectedProvider === 'azure-openai'
                      ? 'Your Azure OpenAI resource URL (per-deployment endpoint)'
                      : 'Custom base URL for this provider'}
                  </p>
                </div>
              )}

              {/* Connection feedback — friendly, actionable message at the display
                  layer; the raw upstream string is preserved behind a collapsible
                  "Technical details" disclosure. role="alert" + aria-live make the
                  failure announced to screen readers (a11y). */}
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
                      {friendlyProbeError(
                        testError,
                        providerDef?.display_name ?? 'the provider',
                      )}
                    </span>
                    {testError && (
                      <details className="text-xs" style={{ color: 'var(--color-muted)' }}>
                        <summary className="cursor-pointer select-none">
                          Technical details
                        </summary>
                        <p className="mt-1 font-mono break-words">{testError}</p>
                      </details>
                    )}
                  </div>
                </div>
              )}

              {/* Connect & Load Models — the main CTA before model selection.
                  Disabled when the key is empty, the test is running, or a
                  required endpoint has not been filled in (azure). */}
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
                    <div className="flex items-center gap-2 text-sm" style={{ color: 'var(--color-success)' }}>
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
                      <p className="text-[10px] mt-1.5" style={{ color: 'var(--color-muted)' }}>
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
        {(() => {
          // Until Connect succeeds AND a model is chosen, render Complete setup
          // with a clearly-disabled outline treatment (not dimmed gold, which
          // reads as enabled on touch). Once enabled it becomes the gold CTA,
          // so the Connect-then-Complete sequence is visually obvious. Scoped
          // to the onboarding CTA — does NOT touch the global button.tsx
          // disabled style.
          return (
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
          )
        })()}
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
          alt="Omnipus — Master Tasker"
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
