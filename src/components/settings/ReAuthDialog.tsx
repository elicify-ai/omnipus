import { useState } from 'react'
import { Eye, EyeSlash, ShieldCheck, SpinnerGap, XCircle } from '@phosphor-icons/react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { reAuth, isApiError } from '@/lib/api'

// ReAuthDialog implements the Spec-6 FR-12.2 consent primitive at the UI layer.
// It prompts the single user to re-type their one password, calls POST
// /auth/reauth, and — on success — hands the minted consent token back to the
// caller via onConfirmed. The caller then replays that token in the
// X-Reauth-Token header on the immediately-following sensitive request.
//
// This is NOT the dev-mode bypass guard (RequireNotBypass, a 503). It is a
// real re-verification of the password before a sensitive settings change.
export function ReAuthDialog({
  open,
  onOpenChange,
  title = 'Confirm your password',
  description = 'For your security, re-type your password to make this change.',
  onConfirmed,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title?: string
  description?: string
  onConfirmed: (token: string) => void
}) {
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const reset = () => {
    setPassword('')
    setShowPassword(false)
    setSubmitting(false)
    setError('')
  }

  const handleOpenChange = (next: boolean) => {
    if (!next) reset()
    onOpenChange(next)
  }

  const handleSubmit = async () => {
    if (!password.trim()) {
      setError('Password is required')
      return
    }
    setSubmitting(true)
    setError('')
    try {
      const resp = await reAuth(password)
      // Wipe the password from state before handing control back.
      setPassword('')
      setSubmitting(false)
      onConfirmed(resp.token)
      onOpenChange(false)
    } catch (err) {
      setSubmitting(false)
      // A wrong password returns 401; surface it inline so the user can retry.
      const message = isApiError(err)
        ? err.status === 401
          ? 'That password is incorrect. Please try again.'
          : err.userMessage
        : err instanceof Error
          ? err.message
          : 'Re-authentication failed'
      setError(message)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ShieldCheck size={18} weight="duotone" className="text-[var(--color-accent)]" />
            {title}
          </DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>

        <div className="space-y-2">
          <Label htmlFor="reauth-password" className="text-xs text-[var(--color-muted)]">
            Password
          </Label>
          <div className="relative">
            <Input
              id="reauth-password"
              type={showPassword ? 'text' : 'password'}
              value={password}
              onChange={(e) => {
                setPassword(e.target.value)
                if (error) setError('')
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !submitting) {
                  e.preventDefault()
                  void handleSubmit()
                }
              }}
              placeholder="Your password"
              autoComplete="current-password"
              autoFocus
              className="pr-10"
              data-testid="reauth-password-input"
            />
            <button
              type="button"
              onClick={() => setShowPassword((v) => !v)}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
              aria-label={showPassword ? 'Hide password' : 'Show password'}
            >
              {showPassword ? <EyeSlash size={15} /> : <Eye size={15} />}
            </button>
          </div>
          {error && (
            <div
              role="alert"
              data-testid="reauth-error"
              className="flex items-start gap-1.5 text-xs text-[var(--color-error)]"
            >
              <XCircle size={13} weight="fill" className="shrink-0 mt-0.5" />
              <span>{error}</span>
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => handleOpenChange(false)} disabled={submitting} data-testid="reauth-cancel">
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={handleSubmit}
            disabled={submitting || !password.trim()}
            data-testid="reauth-confirm"
          >
            {submitting ? (
              <>
                <SpinnerGap size={13} className="animate-spin" /> Verifying…
              </>
            ) : (
              'Confirm'
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
