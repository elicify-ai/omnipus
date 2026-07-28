import { useEffect, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { motion } from 'framer-motion'
import { Eye, EyeSlash, SpinnerGap, ArrowRight, User, Key, Info } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Wordmark } from '@/components/shared/Wordmark'
import { login, fetchAppState, isApiError } from '@/lib/api'
import { useAuthStore } from '@/store/auth'
import { consumeLogoutReason, LOGOUT_REASON_MESSAGE } from '@/lib/authLogout'
import { resetTokenValidationCache } from './authValidation'
import OmnipusAvatar from '@/assets/logo/omnipus-avatar.svg?url'

function LoginScreen() {
  const navigate = useNavigate()
  const storeUsername = useAuthStore((s) => s.setUsername)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [status, setStatus] = useState<'idle' | 'loading' | 'error'>('idle')
  const [error, setError] = useState('')

  // D2 fix: explain an INVOLUNTARY logout (forced by a 401/1008, stashed by
  // forceLogout() — see authLogout.ts). A manual Sidebar "Sign out" never
  // stashes a reason, so this stays null and no banner renders for that
  // path (per the D2 spec's "must not show a scary conflict message" rule).
  // Read via useEffect (not a useState lazy initializer) so React 19
  // StrictMode's double-invoked effects can't clobber the message: the
  // second invocation always finds the key already cleared and no-ops
  // (see consumeLogoutReason's own idempotency doc comment) rather than
  // overwriting the already-set banner with null.
  const [logoutNotice, setLogoutNotice] = useState<string | null>(null)
  useEffect(() => {
    const reason = consumeLogoutReason()
    if (reason) setLogoutNotice(LOGOUT_REASON_MESSAGE[reason])
  }, [])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!username.trim() || !password) return
    setStatus('loading')
    setError('')
    try {
      const resp = await login(username.trim(), password)
      // The gateway has already issued the omnipus-session HttpOnly cookie
      // (US-5) — the SPA only remembers the display-only username.
      storeUsername(resp.username)
      // #359: a fresh login issues a new session cookie — drop any cached
      // validation verdict so the /_app guard re-validates this session
      // immediately.
      resetTokenValidationCache()
      // Check if onboarding is still needed
      const state = await fetchAppState()
      if (!state.onboarding_complete) {
        navigate({ to: '/onboarding' })
      } else {
        navigate({ to: '/' })
      }
    } catch (err) {
      setStatus('error')
      // ApiError.fromResponse already parses {error: "..."} JSON bodies into
      // userMessage, so the old "match status prefix → JSON.parse the rest"
      // dance is no longer necessary. The transport-failure case (status 0)
      // gets a sensible default from the network-error branch.
      if (isApiError(err)) {
        if (err.isNetworkError()) {
          setError('Could not reach the server. Check your connection.')
        } else if (err.status === 401) {
          // A 401 FROM THE LOGIN ENDPOINT means the username/password didn't
          // match — it is NOT an expired session. api-error.ts intentionally
          // maps every 401 to the generic "your session has expired" copy
          // (masking the server's "invalid credentials" to avoid leaking
          // server phrasing), which is correct for authenticated requests but
          // actively misleading on the sign-in form. Show the real cause here.
          setError('Incorrect username or password.')
        } else if (err.status === 429) {
          setError('Too many attempts. Please wait a moment and try again.')
        } else {
          setError(err.userMessage || 'Login failed')
        }
      } else {
        setError('Login failed')
      }
    }
  }

  return (
    <div
      className="min-h-screen flex flex-col items-center justify-center p-6 relative overflow-hidden"
      style={{ backgroundColor: 'var(--color-primary)', color: 'var(--color-secondary)' }}
    >
      {/* Atmospheric depth */}
      <div
        aria-hidden
        className="absolute inset-0 pointer-events-none"
        style={{
          background: 'radial-gradient(ellipse 65% 55% at 50% 50%, rgba(212,175,55,0.055) 0%, transparent 68%)',
        }}
      />

      <motion.div
        initial={{ scale: 0.75, opacity: 0 }}
        animate={{ scale: 1, opacity: 1 }}
        transition={{ duration: 0.5, ease: [0.34, 1.56, 0.64, 1] }}
        className="relative mb-8"
      >
        <div
          aria-hidden
          className="absolute rounded-full blur-3xl pointer-events-none"
          style={{ inset: '-40%', background: 'rgba(212,175,55,0.14)' }}
        />
        <img
          src={OmnipusAvatar}
          alt="Omnipus"
          className="relative h-20 w-20 drop-shadow-2xl"
        />
      </motion.div>

      <motion.div
        initial={{ y: 14, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        transition={{ delay: 0.18, duration: 0.38 }}
        className="w-full max-w-sm z-10"
      >
        <h1 className="font-headline text-3xl font-bold text-center mb-2"
          style={{ color: 'var(--color-secondary)' }}>
          Sign in to <Wordmark />
        </h1>
        <p className="text-sm text-center mb-8" style={{ color: 'var(--color-muted)' }}>
          Enter your username and password
        </p>

        {logoutNotice && (
          <div
            data-testid="logout-notice"
            role="status"
            aria-live="polite"
            className="mb-6 flex items-start gap-2 rounded-lg px-3 py-2.5 text-xs"
            style={{
              backgroundColor: 'rgba(212,175,55,0.08)',
              border: '1px solid rgba(212,175,55,0.25)',
              color: 'var(--color-secondary)',
            }}
          >
            <Info size={14} weight="bold" className="mt-0.5 shrink-0" style={{ color: 'var(--color-accent)' }} />
            <span>{logoutNotice}</span>
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label htmlFor="login-username" className="text-xs font-medium mb-1.5 block"
              style={{ color: 'var(--color-muted)' }}>
              Username
            </label>
            <div className="relative">
              <User size={14} className="absolute left-3 top-1/2 -translate-y-1/2" style={{ color: 'var(--color-muted)' }} />
              <Input
                id="login-username"
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="admin"
                autoComplete="username"
                className="pl-9"
                autoFocus
              />
            </div>
          </div>

          <div>
            <label htmlFor="login-password" className="text-xs font-medium mb-1.5 block"
              style={{ color: 'var(--color-muted)' }}>
              Password
            </label>
            <div className="relative">
              <Key size={14} className="absolute left-3 top-1/2 -translate-y-1/2" style={{ color: 'var(--color-muted)' }} />
              <Input
                id="login-password"
                type={showPassword ? 'text' : 'password'}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Password"
                autoComplete="current-password"
                className="pl-9 pr-9"
              />
              <button tabIndex={0}
                type="button"
                onClick={() => setShowPassword((v) => !v)}
                // Padded 44x44 mobile tap target without enlarging the 14px icon.
                className="absolute right-1 sm:right-2.5 top-1/2 -translate-y-1/2 inline-flex items-center justify-center min-h-11 min-w-11 sm:min-h-0 sm:min-w-0 transition-colors"
                style={{ color: 'var(--color-muted)' }}
                aria-label={showPassword ? 'Hide password' : 'Show password'}
              >
                {showPassword ? <EyeSlash size={14} /> : <Eye size={14} />}
              </button>
            </div>
          </div>

          {status === 'error' && error && (
            <div
              data-testid="login-error"
              role="alert"
              aria-live="assertive"
              className="text-sm text-center"
              style={{ color: 'var(--color-error)' }}
            >
              {error}
            </div>
          )}

          <Button
            type="submit"
            className="w-full h-11 gap-2 font-headline font-bold"
            disabled={!username.trim() || !password || status === 'loading'}
          >
            {status === 'loading' ? (
              <>
                <SpinnerGap size={16} className="animate-spin" />
                Signing in...
              </>
            ) : (
              <>
                Sign in
                <ArrowRight size={16} weight="bold" />
              </>
            )}
          </Button>
        </form>

      </motion.div>
    </div>
  )
}

export const Route = createFileRoute('/login')({
  component: LoginScreen,
})
