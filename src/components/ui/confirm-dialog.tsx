import * as React from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from './alert-dialog'

export interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  description: React.ReactNode
  confirmLabel: React.ReactNode
  cancelLabel?: React.ReactNode
  onConfirm: () => void
  pending?: boolean
  destructive?: boolean
  /**
   * Which button carries the primary visual weight. Defaults to `'confirm'`
   * — the historical, unextended behaviour (Cancel stays `outline`; Confirm
   * is `default`/`destructive`). `'cancel'` inverts that for a
   * safe-choice-is-the-default-action pattern (e.g. a risky-setting
   * consequence dialog, where the confirm action should read as the
   * secondary, de-emphasized choice, not a bold CTA) — Cancel becomes
   * `default`, Confirm becomes `outline` (the `destructive` prop is ignored
   * in this mode, since the emphasized button is Cancel, not Confirm).
   */
  emphasis?: 'confirm' | 'cancel'
}

const ConfirmDialog = ({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  cancelLabel = 'Cancel',
  onConfirm,
  pending = false,
  destructive = false,
  emphasis = 'confirm',
}: ConfirmDialogProps) => {
  const cancelVariant = emphasis === 'cancel' ? 'default' : 'outline'
  const confirmVariant = emphasis === 'cancel' ? 'outline' : destructive ? 'destructive' : 'default'
  const restoreFocusRef = React.useRef<HTMLElement | null>(null)

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent
        aria-busy={pending || undefined}
        onOpenAutoFocus={() => {
          restoreFocusRef.current = document.activeElement instanceof HTMLElement
            ? document.activeElement
            : null
        }}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          const restoreTarget = restoreFocusRef.current
          restoreFocusRef.current = null
          if (restoreTarget?.isConnected) restoreTarget.focus()
        }}
      >
      <AlertDialogHeader>
        <AlertDialogTitle>{title}</AlertDialogTitle>
        <AlertDialogDescription>{description}</AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel data-confirm-dialog-cancel disabled={pending} variant={cancelVariant}>{cancelLabel}</AlertDialogCancel>
        <AlertDialogAction data-confirm-dialog-action disabled={pending} actionState={pending ? 'pending' : 'idle'} variant={confirmVariant} onClick={onConfirm}>
          {confirmLabel}
        </AlertDialogAction>
      </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

ConfirmDialog.displayName = 'ConfirmDialog'

export { ConfirmDialog }
