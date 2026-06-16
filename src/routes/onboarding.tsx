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
  ShieldCheck,
  Lightning,
  Cube,
  User,
  Key,
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ModelSelector, type ModelGroup } from '@/components/ui/model-selector'
import { probeProvider, completeOnboardingTransaction, fetchAppState, isApiError } from '@/lib/api'
import OmnipusAvatar from '@/assets/logo/omnipus-avatar.svg?url'
import { PROVIDER_HINTS } from '@/lib/constants'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'

// First-launch onboarding flow — full-screen, outside AppShell

type Step = 1 | 2 | 3 | 4
type TestStatus = 'idle' | 'testing' | 'success' | 'error'

// All supported providers. Providers with /v1/models get a searchable dropdown;
// providers without it get a text input for manual model slug entry.
const AVAILABLE_PROVIDERS = [
  { id: 'openai', display_name: 'OpenAI' },
  { id: 'openrouter', display_name: 'OpenRouter' },
  { id: 'anthropic', display_name: 'Anthropic' },
  { id: 'google', display_name: 'Google Gemini' },
  { id: 'groq', display_name: 'Groq' },
  { id: 'deepseek', display_name: 'DeepSeek' },
  { id: 'mistral', display_name: 'Mistral' },
  { id: 'azure', display_name: 'Azure OpenAI' },
  { id: 'zhipu', display_name: 'Zhipu' },
  { id: 'moonshot', display_name: 'Moonshot' },
  { id: 'nvidia', display_name: 'NVIDIA' },
  { id: 'minimax', display_name: 'MiniMax' },
  { id: 'qwen', display_name: 'Qwen' },
  { id: 'ollama', display_name: 'Ollama' },
  { id: 'cerebras', display_name: 'Cerebras' },
]

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

const WELCOME_FEATURES = [
  { Icon: ShieldCheck, text: 'Kernel-level sandboxing — agents operate in security boundaries by default' },
  { Icon: Lightning, text: 'Zero-IPC channels — Discord, Slack, Telegram compiled into the binary' },
  { Icon: Cube, text: 'Single Go binary — no runtime dependencies, runs anywhere' },
]

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
export function friendlyProbeError(raw: string, providerName: string): string {
  const r = (raw || '').toLowerCase()
  const has = (...codes: string[]) =>
    codes.some((c) => new RegExp(`(^|[^0-9])${c}([^0-9]|$)`).test(r))
  if (has('401', '403') || /unauthor|forbidden|invalid api key|invalid key|rejected/.test(r)) {
    return `That API key was rejected by ${providerName}. Double-check you copied the full key and that it's active, then retry.`
  }
  if (has('429') || /rate.?limit|too many requests/.test(r)) {
    return `Rate limited by ${providerName}. Wait a moment and retry.`
  }
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

  // Fetch provider list from API for model info; use built-in provider list for onboarding UI

  // Always show all available providers in onboarding, regardless of API results
  const providers = AVAILABLE_PROVIDERS

  const [step, setStep] = useState<Step>(1)
  const [direction, setDirection] = useState(1)
  const [selectedProvider, setSelectedProvider] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [showKey, setShowKey] = useState(false)
  const [testStatus, setTestStatus] = useState<TestStatus>('idle')
  const [testError, setTestError] = useState('')
  const [selectedModel, setSelectedModel] = useState('')
  const [availableModels, setAvailableModels] = useState<string[]>([])
  const [isSaving, setIsSaving] = useState(false)
  // Surfaced inline on the final step when completeOnboardingTransaction fails,
  // so the user stays on the step and can retry rather than failing silently.
  const [finishError, setFinishError] = useState('')
  // Account step (Step 3) — single-user: one username + password
  const [adminUsername, setAdminUsername] = useState('')
  const [adminPassword, setAdminPassword] = useState('')
  const [adminPasswordConfirm, setAdminPasswordConfirm] = useState('')
  const [showAdminPassword, setShowAdminPassword] = useState(false)
  const [adminStatus, setAdminStatus] = useState<'idle' | 'saving' | 'error'>('idle')
  const [adminError, setAdminError] = useState('')

  const providerDef = providers.find((p) => p.id === selectedProvider)
  const providerHintText = selectedProvider ? PROVIDER_HINTS[selectedProvider] : undefined

  const goTo = (next: Step) => {
    setDirection(next > step ? 1 : -1)
    setStep(next)
  }

  const handleSelectProvider = (id: string) => {
    setSelectedProvider(id)
    setApiKey('')
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

  const handleTest = async () => {
    if (!selectedProvider || !apiKey.trim()) return
    setTestStatus('testing')
    setTestError('')
    try {
      // Non-persistent test + fetch: the server probes the provider with the
      // supplied key and returns the model list in one response. Nothing is
      // saved to disk until the user clicks "Complete" on step 4, which fires
      // /onboarding/complete with the full payload atomically.
      const result = await probeProvider(selectedProvider, apiKey.trim())
      if (result.success) {
        setTestStatus('success')
        if (result.models && result.models.length > 0) {
          setAvailableModels(result.models)
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

  const handleFinish = async () => {
    setIsSaving(true)
    setFinishError('')
    try {
      const resp = await completeOnboardingTransaction({
        provider: {
          id: selectedProvider,
          api_key: apiKey,
          model: selectedModel,
        },
        admin: {
          username: adminUsername,
          password: adminPassword,
        },
      })
      useAuthStore.getState().setToken(resp.token, resp.role, resp.username)
      navigate({ to: '/' })
    } catch (err) {
      // Surface the failure both inline (so the user stays on step 4 and can
      // retry) and as a toast — never strand the error silently.
      const message = `Could not complete setup: ${err instanceof Error ? err.message : 'Unknown error'}`
      setFinishError(message)
      addToast({ message, variant: 'error' })
    } finally {
      setIsSaving(false)
    }
  }

  const handleRegisterAdmin = () => {
    if (!adminUsername.trim() || !adminPassword) {
      setAdminError('Username and password are required')
      setAdminStatus('error')
      return
    }
    if (adminPassword.length < 8) {
      setAdminError('Password must be at least 8 characters')
      setAdminStatus('error')
      return
    }
    if (adminPassword !== adminPasswordConfirm) {
      setAdminError('Passwords do not match')
      setAdminStatus('error')
      return
    }
    setAdminStatus('idle')
    setAdminError('')
    goTo(4)
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
          and the sr-only line gives a plain-text "Step X of N" announcement. */}
      <div className="flex flex-col items-center gap-2 mb-12 z-10">
        {/* Visible step counter for sighted users — the dots alone are unlabeled. */}
        <span
          aria-hidden
          className="text-xs font-medium tracking-wide"
          style={{ color: 'var(--color-muted)' }}
        >
          Step {step} of 4
        </span>
        <div
          className="flex items-center gap-2"
          role="progressbar"
          aria-valuenow={step}
          aria-valuemin={1}
          aria-valuemax={4}
          aria-label={`Onboarding progress: step ${step} of 4`}
        >
          <span className="sr-only">Step {step} of 4</span>
          {([1, 2, 3, 4] as Step[]).map((s) => (
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

      {/* Animated step content */}
      <div className="w-full max-w-md z-10">
        <AnimatePresence mode="wait" custom={direction}>
          {step === 1 && (
            <motion.div
              key="step1"
              custom={direction}
              variants={stepVariants}
              initial="enter"
              animate="center"
              exit="exit"
              transition={{ duration: 0.22, ease: 'easeInOut' }}
            >
              <WelcomeStep onGetStarted={() => goTo(2)} />
            </motion.div>
          )}
          {step === 2 && (
            <motion.div
              key="step2"
              custom={direction}
              variants={stepVariants}
              initial="enter"
              animate="center"
              exit="exit"
              transition={{ duration: 0.22, ease: 'easeInOut' }}
            >
              <ProviderStep
                providers={providers}
                selectedProvider={selectedProvider}
                onSelect={handleSelectProvider}
                apiKey={apiKey}
                onApiKeyChange={handleApiKeyChange}
                showKey={showKey}
                onToggleShowKey={() => setShowKey((v) => !v)}
                testStatus={testStatus}
                testError={testError}
                onTest={handleTest}
                onBack={() => goTo(1)}
                onContinue={() => goTo(3)}
                providerHint={providerHintText}
                availableModels={availableModels}
                selectedModel={selectedModel}
                onSelectModel={setSelectedModel}
              />
            </motion.div>
          )}
          {step === 3 && (
            <motion.div
              key="step3"
              custom={direction}
              variants={stepVariants}
              initial="enter"
              animate="center"
              exit="exit"
              transition={{ duration: 0.22, ease: 'easeInOut' }}
            >
              <AdminCredentialsStep
                username={adminUsername}
                onUsernameChange={setAdminUsername}
                password={adminPassword}
                onPasswordChange={setAdminPassword}
                passwordConfirm={adminPasswordConfirm}
                onPasswordConfirmChange={setAdminPasswordConfirm}
                showPassword={showAdminPassword}
                onToggleShowPassword={() => setShowAdminPassword((v) => !v)}
                status={adminStatus}
                error={adminError}
                onRegister={handleRegisterAdmin}
                onBack={() => goTo(2)}
              />
            </motion.div>
          )}
          {step === 4 && (
            <motion.div
              key="step4"
              custom={direction}
              variants={stepVariants}
              initial="enter"
              animate="center"
              exit="exit"
              transition={{ duration: 0.22, ease: 'easeInOut' }}
            >
              <DoneStep
                providerName={providerDef?.display_name ?? selectedProvider}
                isSaving={isSaving}
                onFinish={handleFinish}
                error={finishError}
              />
            </motion.div>
          )}
        </AnimatePresence>
      </div>
    </div>
  )
}

// ── Step 1: Welcome ────────────────────────────────────────────────────────────

function WelcomeStep({ onGetStarted }: { onGetStarted: () => void }) {
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
        <h1 className="font-headline text-4xl sm:text-5xl font-bold leading-tight mb-2"
          style={{ color: 'var(--color-secondary)' }}>
          Welcome to Omnipus
        </h1>
        <p className="font-headline text-base font-bold tracking-wide"
          style={{ color: 'var(--color-accent)' }}>
          Elite Simplicity. Sovereign Control.
        </p>
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.28, duration: 0.38 }}
        className="w-full space-y-2.5"
      >
        {WELCOME_FEATURES.map(({ Icon, text }, i) => (
          <div
            key={i}
            className="flex items-start gap-3 p-3 rounded-lg border text-left"
            style={{
              borderColor: 'var(--color-border)',
              backgroundColor: 'var(--color-surface-1)',
            }}
          >
            <Icon size={17} weight="duotone" className="shrink-0 mt-0.5"
              style={{ color: 'var(--color-accent)' }} />
            <p className="text-sm leading-snug" style={{ color: 'var(--color-muted)' }}>
              {text}
            </p>
          </div>
        ))}
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.38, duration: 0.38 }}
        className="w-full"
      >
        <Button
          className="w-full h-11 gap-2 font-headline font-bold text-base"
          onClick={onGetStarted}
        >
          Get Started
          <ArrowRight size={16} weight="bold" />
        </Button>
      </motion.div>
    </div>
  )
}

// ── Step 2: Provider Setup ─────────────────────────────────────────────────────

function ProviderStep({
  providers,
  selectedProvider,
  onSelect,
  apiKey,
  onApiKeyChange,
  showKey,
  onToggleShowKey,
  testStatus,
  testError,
  onTest,
  onBack,
  onContinue,
  providerHint,
  availableModels,
  selectedModel,
  onSelectModel,
}: {
  providers: { id: string; display_name: string }[]
  selectedProvider: string
  onSelect: (id: string) => void
  apiKey: string
  onApiKeyChange: (k: string) => void
  showKey: boolean
  onToggleShowKey: () => void
  testStatus: TestStatus
  testError: string
  onTest: () => void
  onBack: () => void
  onContinue: () => void
  providerHint?: string
  availableModels: string[]
  selectedModel: string
  onSelectModel: (model: string) => void
}) {
  // Order the rendered provider list with the popular providers first (stable).
  const orderedProviders = sortProvidersByPriority(providers)
  // Build providerGroups for the ModelSelector — single group in onboarding (one provider at a time)
  const providerDef = providers.find((p) => p.id === selectedProvider)
  const providerGroups: ModelGroup[] =
    availableModels.length > 0 && providerDef
      ? [{ providerName: providerDef.display_name, models: availableModels }]
      : []
  return (
    <div className="flex flex-col gap-6">
      <div>
        <h2 className="font-headline text-2xl font-bold mb-1"
          style={{ color: 'var(--color-secondary)' }}>
          Connect a provider
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

              {/* Connect & Load Models — the main CTA before model selection */}
              {testStatus !== 'success' && (
                <Button
                  className="w-full gap-2 font-headline font-bold"
                  onClick={onTest}
                  disabled={!apiKey.trim() || testStatus === 'testing'}
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
                      <ModelSelector
                        models={availableModels}
                        value={selectedModel}
                        onChange={onSelectModel}
                        providerGroups={providerGroups}
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

      {/* Navigation */}
      <div className="flex items-center gap-3 pt-2">
        <Button variant="ghost" className="gap-1.5 min-h-11 sm:min-h-0" onClick={onBack}>
          <ArrowLeft size={14} />
          Back
        </Button>
        {(() => {
          // Until Connect succeeds AND a model is chosen, render Continue with a
          // clearly-disabled ghost/outline treatment (not dimmed gold, which reads
          // as enabled on touch). Once enabled it becomes the gold default CTA, so
          // the Connect-then-Continue sequence is visually obvious. Scoped to the
          // onboarding CTA — does NOT touch the global button.tsx disabled style.
          const continueEnabled = testStatus === 'success' && !!selectedModel.trim()
          return (
            <Button
              variant={continueEnabled ? 'default' : 'outline'}
              className="flex-1 gap-2 font-headline font-bold"
              onClick={onContinue}
              disabled={!continueEnabled}
            >
              Continue
              <ArrowRight size={14} weight="bold" />
            </Button>
          )
        })()}
      </div>
    </div>
  )
}

// ── Step 3: Your Account ───────────────────────────────────────────────────────

function AdminCredentialsStep({
  username,
  onUsernameChange,
  password,
  onPasswordChange,
  passwordConfirm,
  onPasswordConfirmChange,
  showPassword,
  onToggleShowPassword,
  status,
  error,
  onRegister,
  onBack,
}: {
  username: string
  onUsernameChange: (v: string) => void
  password: string
  onPasswordChange: (v: string) => void
  passwordConfirm: string
  onPasswordConfirmChange: (v: string) => void
  showPassword: boolean
  onToggleShowPassword: () => void
  status: 'idle' | 'saving' | 'error'
  error: string
  onRegister: () => void
  onBack: () => void
}) {
  const isValid = username.trim().length > 0 && password.length >= 8 && password === passwordConfirm
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
          Your Account
        </h2>
        <p className="text-sm" style={{ color: 'var(--color-muted)' }}>
          Choose a username and password — this is the one account for your Omnipus
        </p>
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.25, duration: 0.38 }}
        className="w-full space-y-4"
      >
        {/* Username */}
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
          />
        </div>

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
        {status === 'error' && error && (
          <div data-testid="onboarding-error" className="flex items-start gap-2 text-sm" style={{ color: 'var(--color-error)' }}>
            <XCircle size={14} weight="fill" className="shrink-0 mt-0.5" />
            <span>{error}</span>
          </div>
        )}
      </motion.div>

      {/* Navigation */}
      <div className="flex items-center gap-3 pt-2 w-full">
        <Button variant="ghost" className="gap-1.5 min-h-11 sm:min-h-0" onClick={onBack}>
          <ArrowLeft size={14} />
          Back
        </Button>
        <Button
          className="flex-1 gap-2 font-headline font-bold"
          onClick={onRegister}
          disabled={!isValid || status === 'saving'}
        >
          {status === 'saving' ? (
            <>
              <SpinnerGap size={13} className="animate-spin" />
              Setting up...
            </>
          ) : (
            <>
              <Key size={13} weight="duotone" />
              Create Account
            </>
          )}
        </Button>
      </div>
    </div>
  )
}

// ── Step 4: Done ───────────────────────────────────────────────────────────────

function DoneStep({
  providerName,
  isSaving,
  onFinish,
  error,
}: {
  providerName: string
  isSaving: boolean
  onFinish: () => void
  error?: string
}) {
  return (
    <div className="flex flex-col items-center text-center gap-8">
      <motion.div
        initial={{ scale: 0, opacity: 0 }}
        animate={{ scale: 1, opacity: 1 }}
        transition={{ duration: 0.48, ease: [0.34, 1.56, 0.64, 1] }}
      >
        <CheckCircle size={80} weight="fill" style={{ color: 'var(--color-accent)' }} />
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.2, duration: 0.38 }}
      >
        <h2 className="font-headline text-3xl font-bold mb-2"
          style={{ color: 'var(--color-secondary)' }}>
          You&apos;re all set
        </h2>
        <p className="text-sm" style={{ color: 'var(--color-muted)' }}>
          {providerName
            ? `${providerName} connected.`
            : 'Provider connected.'}{' '}
          Omnipus is ready to work.
        </p>
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.32, duration: 0.38 }}
        className="w-full space-y-3"
      >
        {/* Inline failure — keeps the user on this step so they can retry. */}
        {error && (
          <div
            role="alert"
            data-testid="onboarding-error"
            className="flex items-start gap-2 text-sm text-left"
            style={{ color: 'var(--color-error)' }}
          >
            <XCircle size={14} weight="fill" className="shrink-0 mt-0.5" />
            <span>{error}</span>
          </div>
        )}
        <Button
          className="w-full h-11 gap-2 font-headline font-bold text-base"
          onClick={onFinish}
          disabled={isSaving}
        >
          {isSaving ? (
            <>
              <SpinnerGap size={16} className="animate-spin" />
              Loading...
            </>
          ) : error ? (
            <>
              Retry Setup
              <ArrowRight size={16} weight="bold" />
            </>
          ) : (
            <>
              Start Exploring
              <ArrowRight size={16} weight="bold" />
            </>
          )}
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
