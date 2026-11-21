import { useRef, useState } from 'react'
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
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { reAuth, getErrorMessage } from '@/lib/api'

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
  items,
}: {
  /**
   * Settings redesign #1519 (FR-026): the visible labels of every
   * password-marked setting this one prompt covers. When given, the prompt
   * lists them and says that cancelling keeps the unsaved changes.
   */
  items?: string[]
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
  const inputRef = useRef<HTMLInputElement>(null)
  // FR-029: after a refusal the person types again, so focus returns to the
  // password field.
  const refuse = (message: string) => {
    setError(message)
    inputRef.current?.focus()
  }

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
      refuse('Enter your password to continue.')
      return
    }
    setSubmitting(true)
    setError('')
    try {
      const resp = await reAuth(password).finally(() => {
        // FR-028: the typed password is cleared as soon as the one reauth
        // request returns, whatever its result.
        setPassword('')
      })
      setSubmitting(false)
      onConfirmed(resp.token)
      onOpenChange(false)
    } catch (err) {
      setSubmitting(false)
      // A wrong password returns 401; surface it inline so the user can retry.
      const message = getErrorMessage(err, 'Re-authentication failed', {
        // FR-029: wrong password and the reauth rate limit (10 per minute).
        statusOverrides: {
          401: 'The password is incorrect.',
          429: 'Too many tries. Wait a minute, then try again.',
        },
      })
      refuse(message)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-[var(--space-2)]">
            <ShieldCheck size={18} weight="duotone" className="text-[var(--color-accent)]" />
            {title}
          </DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>

        {items && items.length > 0 && (
          <div className="space-y-[var(--space-1)] text-[length:var(--type-body-compact-size)]">
            <p className="text-[var(--color-muted)]">This saves:</p>
            <ul className="list-disc pl-[var(--space-4)] text-[var(--color-secondary)]" data-testid="reauth-items">
              {items.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          </div>
        )}

        <div className="space-y-[var(--space-2)]">
          <Label htmlFor="reauth-password" className="text-[var(--color-muted)]">
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
              ref={inputRef}
              className="pr-[var(--space-6)]"
              data-testid="reauth-password-input"
            />
            <IconButton
              variant="ghost"
              type="button"
              onClick={() => setShowPassword((v) => !v)}
              className="absolute right-2 top-1/2 h-auto w-auto -translate-y-1/2 p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
              aria-label={showPassword ? 'Hide password' : 'Show password'}
            >
              {showPassword ? <EyeSlash size={15} /> : <Eye size={15} />}
            </IconButton>
          </div>
          {items && items.length > 0 && (
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
              If you cancel, your unsaved changes stay on the page.
            </p>
          )}
          {error && (
            <div
              role="alert"
              data-testid="reauth-error"
              className="flex items-start gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-error)]"
            >
              <XCircle size={13} weight="fill" className="shrink-0 mt-[var(--space-0-5)]" />
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
            // Confirm stays pressable: an empty password is refused inline
            // with a message and focus in the field (D-11 row 3, FR-029).
            disabled={submitting}
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
