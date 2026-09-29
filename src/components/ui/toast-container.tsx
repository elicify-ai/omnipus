import { X, CheckCircle, Warning, WarningCircle } from '@phosphor-icons/react'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'

export function ToastContainer() {
  const { toasts, removeToast } = useUiStore()

  if (toasts.length === 0) return null

  return (
    <div className="fixed bottom-4 right-4 z-[100] flex flex-col gap-[var(--space-2)] max-w-sm w-full pointer-events-none">
      {toasts.map((toast) => {
        return (
        <div
          key={toast.id}
          data-testid={toast.testId}
          // ARIA APG toast pattern: a toast is a transient status message,
          // not a modal dialog. Errors must be announced immediately
          // (role="alert" carries implicit aria-live="assertive"); info,
          // success, and warning use role="status" with implicit
          // aria-live="polite" so they don't interrupt the user. Inlined as a
          // literal ternary (rather than a `toastRole` local) so the
          // controls/raw-button scanner can statically prove every branch is
          // outside {button,radio,switch,tab} — a variable alias of the same
          // ternary is invisible to it and fails closed (see
          // scripts/design-system-locks/controls.mjs, FIX-ROLEBUTTON notes,
          // which name this exact call site).
          role={toast.variant === 'error' ? 'alert' : 'status'}
          className={cn(
            'flex items-start gap-[var(--space-2-5)] rounded-lg border px-[var(--space-3)] py-[var(--space-2-5)] shadow-lg pointer-events-auto',
            'animate-in slide-in-from-bottom-2 fade-in',
            toast.variant === 'error'
              ? 'bg-[var(--color-surface-2)] border-[var(--color-error)]/30 text-[var(--color-secondary)]'
              : toast.variant === 'success'
              ? 'bg-[var(--color-surface-2)] border-[var(--color-success)]/30 text-[var(--color-secondary)]'
              : toast.variant === 'warning'
              ? 'bg-[var(--color-surface-2)] border-[var(--color-accent)]/30 text-[var(--color-secondary)]'
              : 'bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)]'
          )}
        >
          {toast.variant === 'error' && (
            <WarningCircle size={16} className="text-[var(--color-error)] shrink-0 mt-[var(--space-0-5)]" weight="fill" />
          )}
          {toast.variant === 'success' && (
            <CheckCircle size={16} className="text-[var(--color-success)] shrink-0 mt-[var(--space-0-5)]" weight="fill" />
          )}
          {toast.variant === 'warning' && (
            <Warning size={16} className="text-[var(--color-accent)] shrink-0 mt-[var(--space-0-5)]" weight="fill" />
          )}
          <p className="flex-1 text-[length:var(--type-body-compact-size)]">{toast.message}</p>
          {toast.action && (
            <Button
              variant="ghost"
              onClick={() => {
                toast.action!.onClick()
                removeToast(toast.id)
              }}
              className="h-auto w-auto shrink-0 p-0 text-[length:var(--type-utility-xs-size)] font-medium text-[var(--color-accent)] hover:bg-transparent hover:text-[var(--color-accent)] hover:underline"
            >
              {toast.action.label}
            </Button>
          )}
          <IconButton
            onClick={() => removeToast(toast.id)}
            aria-label="Dismiss"
            className="h-auto w-auto shrink-0 p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
          >
            <X size={14} />
          </IconButton>
        </div>
        )
      })}
    </div>
  )
}
