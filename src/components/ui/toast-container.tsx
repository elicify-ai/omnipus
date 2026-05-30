import { X, CheckCircle, Warning, WarningCircle } from '@phosphor-icons/react'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'

export function ToastContainer() {
  const { toasts, removeToast } = useUiStore()

  if (toasts.length === 0) return null

  return (
    <div className="fixed bottom-4 right-4 z-[100] flex flex-col gap-2 max-w-sm w-full pointer-events-none">
      {toasts.map((toast) => (
        <div
          key={toast.id}
          data-testid={toast.testId}
          className={cn(
            'flex items-start gap-3 rounded-lg border px-4 py-3 shadow-lg pointer-events-auto',
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
            <WarningCircle size={16} className="text-[var(--color-error)] shrink-0 mt-0.5" weight="fill" />
          )}
          {toast.variant === 'success' && (
            <CheckCircle size={16} className="text-[var(--color-success)] shrink-0 mt-0.5" weight="fill" />
          )}
          {toast.variant === 'warning' && (
            <Warning size={16} className="text-[var(--color-accent)] shrink-0 mt-0.5" weight="fill" />
          )}
          <p className="flex-1 text-sm">{toast.message}</p>
          <button
            type="button"
            onClick={() => removeToast(toast.id)}
            className="text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors shrink-0"
            aria-label="Dismiss"
          >
            <X size={14} />
          </button>
        </div>
      ))}
    </div>
  )
}
